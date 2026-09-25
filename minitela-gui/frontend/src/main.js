import './style.css';
import './app.css';
import {
    Connect, Disconnect, IsConnected,
    SetBacklight, WriteText,
    StartMonitor, StopMonitor, GetSystemStats,
    GoToPage, GoToImageSlot,
    GetNoteRules, SetNoteRules,
    GetSchedules, SetSchedules,
    GetWeatherConfig, SetWeatherConfig,
    AutoStartEnabled, SetAutoStartEnabled, CreateShortcut,
    UploadGifFile, UploadGifFromPath, RestoreTheme, UploadImageToTheme,
} from '../wailsjs/go/main/App';
import { EventsOn, BrowserOpenURL } from '../wailsjs/runtime/runtime';

const $ = (id) => document.getElementById(id);

const MAX = 100;
const CONNECT_ON_START = 'connectOnStart';

// ---- state ----
let monitorOn = false;
let currentBacklight = 60;
let currentText = '';
let connected = false;

// ---- helpers ----
function toast(msg, kind = 'ok') {
    const wrap = $('toastWrap');
    const el = document.createElement('div');
    el.className = 'toast ' + kind;
    el.textContent = msg;
    wrap.appendChild(el);
    setTimeout(() => el.remove(), 3200);
}

function setPreview(text) {
    $('previewText').textContent = text || ' ';
}

function setConn(on) {
    connected = on;
    const dot = $('connDot');
    dot.className = 'conn-dot ' + (on ? 'on' : 'off');
    $('connText').textContent = on ? 'Conectada' : 'Desconectado';
}

function refreshCharCount() {
    const v = $('textInput').value;
    $('charCount').textContent = `${v.length} / ${MAX}`;
    currentText = v;
}

// ---- nav ----
document.querySelectorAll('.nav-item').forEach((btn) => {
    btn.addEventListener('click', () => {
        document.querySelectorAll('.nav-item').forEach((b) => b.classList.remove('active'));
        btn.classList.add('active');
        document.querySelectorAll('.view').forEach((v) => v.classList.remove('active'));
        $('view-' + btn.dataset.view).classList.add('active');
    });
});

// ---- connect on load ----
async function tryConnect() {
    try {
        const on = await IsConnected();
        if (on) { setConn(true); startMonitorAuto(); return; }
        await Connect('');
        setConn(true);
        startMonitorAuto();
        toast('Conectada à mini tela');
    } catch (e) {
        setConn(false);
        toast('Não foi possível conectar: ' + String(e), 'err');
    }
}

// Auto-starts the hidden data loop right after a successful connection so the
// mini screen keeps updating as soon as the app opens (sem intervenção do usuário).
async function startMonitorAuto() {
    if (monitorOn) return;
    try {
        // 10s: equilíbrio entre atualização ágil do monitor e não sobrecargar
        // o firmware; a tela redibuja os íconos de batería/WiFi em cada ciclo.
        await StartMonitor(10);
        monitorOn = true;
        refreshStats();
    } catch (e) {
        /* monitor já em execução ou sem dispositivo: ignora */
    }
}

// ---- brightness ----
function renderBla(v) {
    $('brightness').value = v;
    $('brightnessVal').textContent = v + '%';
    currentBacklight = v;
    const bars = document.querySelectorAll('.screen-bars i');
    const levels = [0.3, 0.55, 0.8, 1];
    bars.forEach((b, i) => {
        b.style.opacity = v === 0 ? 0.05 : (0.35 + levels[i] * 0.65 * (v / 100));
    });
}

$('brightness').addEventListener('input', (e) => {
    const v = Number(e.target.value);
    $('brightnessVal').textContent = v + '%';
    currentBacklight = v;
    if (v === 0) setPreview(' ');
});

document.querySelectorAll('[data-brl]').forEach((b) => {
    b.addEventListener('click', () => renderBla(Number(b.dataset.brl)));
});

$('btnPreviewBright').addEventListener('click', async () => {
    if (!connected) { tryConnect(); }
    try {
        await SetBacklight(currentBacklight);
        toast(`Brilho definido para ${currentBacklight}%`);
    } catch (e) {
        toast('Falha ao ajustar brilho: ' + String(e), 'err');
    }
});

// ---- send text ----
$('textInput').addEventListener('input', refreshCharCount);

$('btnSend').addEventListener('click', async () => {
    if (!currentText) { toast('Digite um texto primeiro', 'err'); return; }
    if (!connected) { tryConnect(); }
    try {
        await WriteText(currentText, currentBacklight);
        setPreview(currentText);
        toast('Texto enviado');
    } catch (e) {
        toast('Falha: ' + String(e), 'err');
    }
});

// ---- monitor ----
async function refreshStats() {
    try {
        const g = await GetSystemStats();
        $('statCpu').innerHTML = `${g.cpu}<small>%</small>`;
        $('statBat').innerHTML = `${g.battery}<small>%</small>`;
        $('statWifi').textContent = g.wifiSSID || '-';
        $('statWifiSig').textContent = 'sinal: ' + (g.wifiSig || '-');
    } catch (e) {
        /* ignore */
    }
}

EventsOn('stats', (g) => {
    try {
        if (g && g.cpu !== undefined) $('statCpu').innerHTML = `${g.cpu}<small>%</small>`;
        if (g && g.battery !== undefined) $('statBat').innerHTML = `${g.battery}<small>%</small>`;
        if (g && g.wifiSSID !== undefined) $('statWifi').textContent = g.wifiSSID;
        if (g && g.wifiSig !== undefined) $('statWifiSig').textContent = 'sinal: ' + g.wifiSig;
    } catch (e) { /* ignore */ }
});

// Page id (register 2 value / pageId) -> screen. Confirmed on hardware: the
// physical key cycles WhatsApp=1, Notas=2, Monitor=3, Clima=4, Imagem=5.
EventsOn('page', (p) => {
    if (p === undefined) return;
    // Apenas destaca o botão correspondente no seletor de telas. Não mexe na
    // navegação do app: o usuário continua na aba que escolheu.
    document.querySelectorAll('.page-btn[data-page]').forEach((b) => {
        b.classList.toggle('active-page', Number(b.dataset.page) === Number(p));
    });
});

// ---- settings ----
function setSwitch(el, on) {
    el.classList.toggle('on', on);
    el.setAttribute('aria-checked', String(on));
}

$('swAutoStart').addEventListener('click', async () => {
    const sw = $('swAutoStart');
    const on = !sw.classList.contains('on');
    // optimistically toggle, revert on error
    setSwitch(sw, on);
    try {
        const r = await SetAutoStartEnabled(on);
        toast(on ? 'Inicialização automática ativada' : 'Inicialização automática desativada');
    } catch (e) {
        setSwitch(sw, !on);
        toast('Falha: ' + String(e), 'err');
    }
});

$('swConnect').addEventListener('click', () => {
    const sw = $('swConnect');
    const on = !sw.classList.contains('on');
    setSwitch(sw, on);
    localStorage.setItem(CONNECT_ON_START, on ? '1' : '0');
    if (on) {
        tryConnect().then(() => { if (connected) startMonitorAuto(); });
    } else {
        StopMonitor();
        monitorOn = false;
        Disconnect().then(() => setConn(false));
    }
});

// "Reconectar": desliga o monitor, fecha a conexão atual e abre de novo
// (útil quando a mini tela travou ou caiu da porta USB).
$('btnReconnect').addEventListener('click', async () => {
    const btn = $('btnReconnect');
    btn.classList.add('spinning');
    btn.disabled = true;
    try {
        await StopMonitor();
        monitorOn = false;
        await Disconnect();
        await Connect('');
        setConn(true);
        startMonitorAuto();
        toast('Reconectada à mini tela');
    } catch (e) {
        setConn(false);
        toast('Falha na reconexão: ' + String(e), 'err');
    } finally {
        btn.classList.remove('spinning');
        btn.disabled = false;
    }
});

// ---- page selectors (alternar entre as telas da minitela) ----
// Hardware pages are fixed (1=WhatsApp, 2=Notas, 3=Monitor, 4=Clima, 5=Imagem)
// but WhatsApp is disabled, so only pages 2-5 are selectable.
const PAGE_NAMES = { 2: 'Notas', 3: 'Monitor', 4: 'Clima', 5: 'Imagem' };
let pageBusy = false;
function bindPageSelectors() {
    document.querySelectorAll('.page-btn[data-page]').forEach((btn) => {
        btn.addEventListener('click', async () => {
            if (pageBusy) return; // drop rapid clicks while a switch is in flight
            pageBusy = true;
            btn.classList.add('disabled');
            try {
                if (!connected) { tryConnect(); }
                await GoToPage(Number(btn.dataset.page));
                toast('Tela alterada para ' + PAGE_NAMES[Number(btn.dataset.page)]);
            } catch (e) {
                toast('Falha ao trocar de tela: ' + String(e), 'err');
            } finally {
                pageBusy = false;
                btn.classList.remove('disabled');
            }
        });
    });
}

// ---- notas (3 lembretes: texto + repetição uma vez / todo dia / semanal) ----
function noteModeEls(i) {
    return {
        text: $('note' + i + 'Text'),
        mode: $('note' + i + 'Mode'),
        once: $('note' + i + 'Once'),
        time: $('note' + i + 'Time'),
        week: $('note' + i + 'Week'),
    };
}

function weekdayMaskFromWeek(weekEl) {
    let mask = 0;
    weekEl.querySelectorAll('.day-btn').forEach((b) => {
        if (b.classList.contains('on')) mask |= 1 << Number(b.dataset.day);
    });
    return mask;
}

function setWeekMask(weekEl, mask) {
    weekEl.querySelectorAll('.day-btn').forEach((b) => {
        b.classList.toggle('on', (mask & (1 << Number(b.dataset.day))) !== 0);
    });
}

function applyNoteMode(i) {
    const el = noteModeEls(i);
    const mode = el.mode.value;
    el.once.style.display = mode === 'once' ? '' : 'none';
    el.time.style.display = mode === 'once' ? 'none' : '';
    el.week.style.display = mode === 'weekly' ? '' : 'none';
}

async function loadNotesView() {
    for (let i = 1; i <= 3; i++) applyNoteMode(i);
    try {
        const saved = await GetNoteRules();
        if (!Array.isArray(saved)) return;
        for (let i = 0; i < saved.length && i < 3; i++) {
            const r = saved[i];
            const el = noteModeEls(i + 1);
            el.text.value = r.text || '';
            el.mode.value = r.mode || 'once';
            el.once.value = r.onceAt || '';
            el.time.value = r.time || '';
            setWeekMask(el.week, r.weekdayBit || 0);
            applyNoteMode(i + 1);
        }
    } catch (e) { /* ignore */ }
}

$('btnSaveNotes').addEventListener('click', async () => {
    if (!connected) { tryConnect(); }
    try {
        const rules = [];
        for (let i = 1; i <= 3; i++) {
            const el = noteModeEls(i);
            const mode = el.mode.value;
            rules.push({
                text: el.text.value,
                mode: mode,
                onceAt: mode === 'once' ? el.once.value : '',
                time: mode === 'once' ? '' : el.time.value,
                weekdayBit: mode === 'weekly' ? weekdayMaskFromWeek(el.week) : 0,
            });
        }
        await SetNoteRules(rules);
        toast('Notas salvas (disparam e mudam a tela para Notas; o texto fica até você trocar de tela)');
    } catch (e) {
        toast('Falha: ' + String(e), 'err');
    }
});

for (let i = 1; i <= 3; i++) {
    (function (idx) {
        const el = noteModeEls(idx);
        el.mode.addEventListener('change', () => applyNoteMode(idx));
        el.week.querySelectorAll('.day-btn').forEach((b) => {
            b.addEventListener('click', () => b.classList.toggle('on'));
        });
    })(i);
}

// ---- agenda (pré-definições diárias: horário -> tela + brilho) ----
const SCHED_PAGES = { 2: 'Notas', 3: 'Monitor', 4: 'Clima', 5: 'Imagem' };
let scheduleRules = [];

function scheduleRowHTML(r, i) {
    const pageOpts = Object.entries(SCHED_PAGES)
        .map(([id, name]) => `<option value="${id}" ${Number(r.page) === Number(id) ? 'selected' : ''}>${name}</option>`)
        .join('');
    return `
        <div class="sched-row" data-i="${i}">
            <button class="switch ${r.enabled ? 'on' : ''}" role="switch" aria-checked="${r.enabled ? 'true' : 'false'}"></button>
            <input type="time" class="sched-start" value="${r.start || ''}"/>
            <input type="time" class="sched-end" value="${r.end || ''}"/>
            <select class="sched-page">${pageOpts}</select>
            <div class="sched-bright">
                <input type="range" min="0" max="100" value="${r.brightness}" class="sched-brightness"/>
                <span class="val">${r.brightness}%</span>
            </div>
            <button class="btn-remove-rule" title="Remover">✕</button>
        </div>`;
}

function renderSchedule() {
    const wrap = $('scheduleList');
    if (!scheduleRules.length) {
        wrap.innerHTML = '<div class="sched-empty">Nenhuma janela configurada. Clique em "Adicionar janela" para criar uma.</div>';
        return;
    }
    wrap.innerHTML = scheduleRules.map(scheduleRowHTML).join('');

    wrap.querySelectorAll('.sched-row').forEach((row) => {
        const i = Number(row.dataset.i);
        row.querySelector('.switch').addEventListener('click', () => {
            scheduleRules[i].enabled = !scheduleRules[i].enabled;
            renderSchedule();
        });
        row.querySelector('.sched-start').addEventListener('change', (e) => {
            scheduleRules[i].start = e.target.value;
        });
        row.querySelector('.sched-end').addEventListener('change', (e) => {
            scheduleRules[i].end = e.target.value;
        });
        row.querySelector('.sched-page').addEventListener('change', (e) => {
            scheduleRules[i].page = Number(e.target.value);
        });
        row.querySelector('.sched-brightness').addEventListener('input', (e) => {
            scheduleRules[i].brightness = Number(e.target.value);
            row.querySelector('.sched-bright .val').textContent = e.target.value + '%';
        });
        row.querySelector('.btn-remove-rule').addEventListener('click', () => {
            scheduleRules.splice(i, 1);
            renderSchedule();
        });
    });
}

async function loadScheduleView() {
    try {
        scheduleRules = await GetSchedules();
        if (!Array.isArray(scheduleRules)) scheduleRules = [];
        scheduleRules = scheduleRules.map((r) => ({
            enabled: !!r.enabled,
            start: r.start || '',
            end: r.end || '',
            page: r.page || 3,
            brightness: r.brightness == null ? 60 : r.brightness,
        }));
        renderSchedule();
    } catch (e) { /* ignore */ }
}

$('btnAddRule').addEventListener('click', () => {
    scheduleRules.push({ enabled: true, start: '13:00', end: '18:00', page: 3, brightness: 10 });
    renderSchedule();
});

$('btnSaveSchedules').addEventListener('click', async () => {
    try {
        const valid = scheduleRules
            .filter((r) => /^\d{2}:\d{2}$/.test(r.start || '') && /^\d{2}:\d{2}$/.test(r.end || ''))
            .map((r) => ({ enabled: r.enabled, start: r.start, end: r.end, page: r.page, brightness: r.brightness }));
        await SetSchedules(valid);
        scheduleRules = valid;
        renderSchedule();
        toast(`Pré-definições salvas (${valid.length})`);
    } catch (e) {
        toast('Falha ao salvar: ' + String(e), 'err');
    }
});

EventsOn('schedule-run', (d) => {
    if (d && d.start) {
        toast(`Agenda ${d.start}–${d.end}: ${d.page} · brilho ${d.brightness}%`);
    }
});

$('btnShortcut').addEventListener('click', async () => {
    try {
        await CreateShortcut();
        toast('Atalho criado na área de trabalho');
    } catch (e) {
        toast('Falha ao criar atalho: ' + String(e), 'err');
    }
});

$('btnInstagram').addEventListener('click', () => {
    BrowserOpenURL('https://www.instagram.com/eduardospek');
});

// ---- clima ----
const WEATHER_ICONS = ['☀', '⛅', '☁', '🌧', '☾', '☾⛅', '❄', '·'];
function renderWeather(d) {
    const grid = $('weatherGrid');
    if (!d || !d.days || !d.days.length) {
        grid.innerHTML = '<div class="empty-hint" id="weatherEmpty">Sem dados ainda.</div>';
        $('weatherPlace').textContent = d && d.placeName ? d.placeName : '';
        return;
    }
    if (d.placeName) $('weatherPlace').textContent = '· ' + d.placeName;
    const empty = $('weatherEmpty');
    if (empty) empty.remove();
    const hint = $('weatherHint');
    if (hint) hint.style.display = 'none';
    grid.innerHTML = d.days.map((day) => {
        const ic = WEATHER_ICONS[Math.max(0, Math.min(7, Number(day.icon)))];
        return `<div class="wday">
            <div class="wd">${day.weekday}</div>
            <div class="wi">${ic}</div>
            <div class="wt">${Math.round(day.tempMax)}°<small>/${Math.round(day.tempMin)}°</small></div>
            <div class="wd-desc">${day.condition}</div>
        </div>`;
    }).join('');
}

async function refreshWeatherView() {
    try {
        const cfg = await GetWeatherConfig();
        $('weatherCity').value = cfg.city || '';
        if (cfg.placeName) $('weatherPlace').textContent = '· ' + cfg.placeName;
    } catch (e) { /* ignore */ }
}

$('btnSaveWeather').addEventListener('click', async () => {
    const city = $('weatherCity').value.trim();
    if (!city) { toast('Informe o nome da cidade', 'err'); return; }
    if (!connected) { tryConnect(); }
    const btn = $('btnSaveWeather');
    btn.classList.add('disabled');
    try {
        await SetWeatherConfig(city);
        toast('Clima atualizado para: ' + city);
    } catch (e) {
        toast('Falha ao atualizar o clima: ' + String(e), 'err');
    } finally {
        btn.classList.remove('disabled');
    }
});

EventsOn('weather', (d) => renderWeather(d));

// ---- imagem ----
$('btnRestoreTheme').addEventListener('click', async () => {
    if (!connected) { tryConnect(); }
    const btn = $('btnRestoreTheme');
    btn.classList.add('disabled');
    $('imageStatus').textContent = 'Enviando tema de teste (OTA)...';
    try {
        await RestoreTheme();
        $('imageStatus').textContent = 'Tema restaurado; minitela de volta ao Monitor.';
        toast('Tema restaurado');
    } catch (e) {
        $('imageStatus').textContent = 'Erro: ' + String(e);
        toast('Falha no envio OTA: ' + String(e), 'err');
    } finally {
        btn.classList.remove('disabled');
    }
});

$('btnUploadImage').addEventListener('click', async () => {
    const file = $('imageFile').files && $('imageFile').files[0];
    if (!file) { toast('Escolha um arquivo primeiro', 'err'); return; }
    const page = imagePage;
    if (!page) { toast('Escolha a página de imagem', 'err'); return; }
    if (!connected) { tryConnect(); }
    const btn = $('btnUploadImage');
    btn.classList.add('disabled');
        $('imageStatus').textContent = `Convertendo e enviando à página Imagem ${page}... (a minitela vai reiniciar)`;
    try {
        const bytes = new Uint8Array(await file.arrayBuffer());
        await UploadImageToTheme(Array.from(bytes), page);
            $('imageStatus').textContent = `Imagem aplicada e exibida na mini tela (slot ${page}).`;
            toast(`Imagem ${page} aplicada e exibida`);
    } catch (e) {
        $('imageStatus').textContent = 'Erro: ' + String(e);
        toast('Falha no envio: ' + String(e), 'err');
    } finally {
        btn.classList.remove('disabled');
    }
});

$('btnSendTestGif') &&
    $('btnSendTestGif').addEventListener('click', async () => {
        if (!connected) { tryConnect(); }
        const btn = $('btnSendTestGif');
        btn.classList.add('disabled');
        $('imageStatus').textContent = 'Enviando GIF de teste (raw 192x192)...';
        try {
            const testPath = 'C:\\Users\\spekv\\minitela-go\\test_gif.gif';
            await UploadGifFromPath(testPath);
            $('imageStatus').textContent = 'GIF de teste enviado e exibido na página Imagem.';
            toast('GIF de teste enviado');
        } catch (e) {
            $('imageStatus').textContent = 'Erro: ' + String(e);
            toast('Falha: ' + String(e), 'err');
        } finally {
            btn.classList.remove('disabled');
        }
    });

const imagePageSeg = $('imagePageSeg');
let imagePage = 1;
if (imagePageSeg) {
    imagePageSeg.addEventListener('click', (e) => {
        const btn = e.target.closest('.seg-btn');
        if (!btn) return;
        imagePageSeg.querySelectorAll('.seg-btn').forEach((b) => b.classList.remove('is-active'));
        btn.classList.add('is-active');
        imagePage = parseInt(btn.dataset.imgPage, 10) || 1;
    });
}

// "Exibir Imagem N": mostra o slot N (Gif1/2/3) na mini tela escrevendo o
// índice da página do tema (4/5/6) no registrador de página.
document.querySelectorAll('[data-img-slot]').forEach((btn) => {
    btn.addEventListener('click', async () => {
        const slot = parseInt(btn.dataset.imgSlot, 10) || 1;
        if (!connected) { tryConnect(); }
        try {
            await GoToImageSlot(slot);
            toast(`Exibindo Imagem ${slot} na mini tela`);
        } catch (e) {
            toast('Falha ao exibir: ' + String(e), 'err');
        }
    });
});

// ---- init ----
async function loadAutoStartState() {
    try {
        const on = await AutoStartEnabled();
        setSwitch($('swAutoStart'), on);
    } catch (e) { /* ignore */ }
    // "Conectar na inicialização" is a UI preference kept in localStorage so it
    // survives app restarts (it is not the Windows registry auto-start above).
    setSwitch($('swConnect'), localStorage.getItem(CONNECT_ON_START) === '1');
}

(async function init() {
    renderBla(60);
    refreshCharCount();
    setPreview('Minitela Go');
    await loadAutoStartState();
    bindPageSelectors();
    refreshWeatherView();
    loadNotesView();
    loadScheduleView();
    // Respect the "Conectar na inicialização" preference: connect on launch only
    // when the user enabled it, otherwise leave the device off until asked.
    if (localStorage.getItem(CONNECT_ON_START) === '1') tryConnect();
})();
