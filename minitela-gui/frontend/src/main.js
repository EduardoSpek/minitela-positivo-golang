import './style.css';
import './app.css';
import {
    Connect, Disconnect, IsConnected,
    SetBacklight, WriteText, SetDateTime,
    StartMonitor, StopMonitor, GetSystemStats,
    GoToPage, SetNotes,
    AutoStartEnabled, SetAutoStartEnabled, CreateShortcut,
} from '../wailsjs/go/main/App';
import { EventsOn } from '../wailsjs/runtime/runtime';

const $ = (id) => document.getElementById(id);

const MAX = 100;

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

// Auto-starts the data loop right after a successful connection so the mini
// screen keeps updating as soon as the app opens.
async function startMonitorAuto() {
    if (monitorOn) return;
    try {
        await StartMonitor(5);
        monitorOn = true;
        $('btnMonitorToggle').textContent = 'Parar';
        $('monitorHint').textContent = 'Enviando dados para a tela ativa automaticamente.';
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

$('btnSendDate').addEventListener('click', async () => {
    if (!connected) { tryConnect(); }
    try {
        const r = await SetDateTime();
        toast('Data/hora enviadas');
    } catch (e) {
        toast('Falha: ' + String(e), 'err');
    }
});

// ---- monitor ----
$('btnMonitorToggle').addEventListener('click', async () => {
    if (monitorOn) {
        StopMonitor();
        monitorOn = false;
        $('btnMonitorToggle').textContent = 'Iniciar';
        $('monitorHint').textContent = 'Monitor parado.';
        toast('Envio de dados parado');
        return;
    }
    if (!connected) { tryConnect(); }
    try {
        await StartMonitor(5);
        monitorOn = true;
        $('btnMonitorToggle').textContent = 'Parar';
        $('monitorHint').textContent = 'Monitorando... os dados aparecem na tela ativa.';
        toast('Envio de dados iniciado');
        await refreshStats();
    } catch (e) {
        toast('Falha ao iniciar: ' + String(e), 'err');
    }
});

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
const PAGE_SCREENS = {
    1: 'screen',   // whatsapp (adiado para depois)
    2: 'notes',    // notas
    3: 'monitor',  // estatísticas
    4: 'screen',   // clima
    5: 'screen',   // imagem
};

EventsOn('page', (p) => {
    if (p === undefined) return;
    // Highlight the matching page selector.
    document.querySelectorAll('[data-page]').forEach((b) => {
        b.classList.toggle('active-page', Number(b.dataset.page) === Number(p));
    });
    const target = PAGE_SCREENS[Number(p)];
    if (!target) return;
    document.querySelectorAll('.nav-item').forEach((b) => b.classList.remove('active'));
    document.querySelectorAll('.view').forEach((v) => v.classList.remove('active'));
    document.querySelector(`.nav-item[data-view="${target}"]`)?.classList.add('active');
    document.querySelector(`#view-${target}`)?.classList.add('active');
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
    if (on) {
        tryConnect().then(() => { if (connected) startMonitorAuto(); });
    } else {
        StopMonitor();
        monitorOn = false;
        $('btnMonitorToggle').textContent = 'Iniciar';
        Disconnect().then(() => setConn(false));
    }
});

// ---- page selectors (alternar entre as 5 telas da minitela) ----
const PAGE_NAMES = ['WhatsApp', 'Notas', 'Monitor', 'Clima', 'Imagem'];
function bindPageSelectors() {
    document.querySelectorAll('[data-page]').forEach((btn) => {
        btn.addEventListener('click', async () => {
            if (!connected) { tryConnect(); }
            try {
                await GoToPage(Number(btn.dataset.page));
                toast('Tela alterada para ' + PAGE_NAMES[Number(btn.dataset.page) - 1]);
            } catch (e) {
                toast('Falha ao trocar de tela: ' + String(e), 'err');
            }
        });
    });
}

// ---- notas (3 lembretes: texto + horário) ----
function collectNotes() {
    return [
        [$('note1Text').value, $('note1Time').value],
        [$('note2Text').value, $('note2Time').value],
        [$('note3Text').value, $('note3Time').value],
    ];
}
$('btnSaveNotes').addEventListener('click', async () => {
    if (!connected) { tryConnect(); }
    try {
        const [n1, n2, n3] = collectNotes();
        await SetNotes(n1[0], n1[1], n2[0], n2[1], n3[0], n3[1]);
        toast('Notas salvas (enviadas quando a tela de Notas estiver ativa)');
    } catch (e) {
        toast('Falha: ' + String(e), 'err');
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

// ---- init ----
async function loadAutoStartState() {
    try {
        const on = await AutoStartEnabled();
        setSwitch($('swAutoStart'), on);
    } catch (e) { /* ignore */ }
}

(function init() {
    renderBla(60);
    refreshCharCount();
    setPreview('Minitela Go');
    loadAutoStartState();
    bindPageSelectors();
    tryConnect();
})();
