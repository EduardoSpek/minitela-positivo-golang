# Minitela Go

Controlador para a **mini tela** dos notebooks Positivo (1.54" IPS 240×240, abaixo do teclado) — escrito em Go, com interface gráfica nativa para Windows.

O projeto nasceu de engenharia reversa do app oficial `MiniTelaApp` (Electron + protocolo UART) e do projeto de referência `open-minitela-R15M`.

> Feche o app oficial `MiniTelaApp` antes de usar o Minitela Go: a mini tela só aceita um programa por vez.

---

## Créditos

Desenvolvido por **Eduardo Spek** — [www.instagram.com/eduardospek](https://www.instagram.com/eduardospek) (`@eduardospek`)

---

## Funcionalidades

**Tela**
- Brilho de 0 a 100% com pré-visualização e envio à minitela.
- Envio de texto ASCII para a página de notas.

**Monitor**
- Bateria, Wi-Fi (SSID + sinal) e Bluetooth enviados continuamente para a minitela.
- Relógio no topo da tela, sincronizado com o PC.

**Notas**
- **1 lembrete** com repetição: *uma vez* (data/hora), *todo dia* (hora) ou *dias da semana* (hora + dias marcados).
- Ao disparar, o app vira a tela para Notas e o **texto permanece até você trocar de tela**.

**Clima**
- Previsão de 5 dias no painel do app (Open-Meteo, sem chave de API).
- Na minitela: condição de hoje + 2 dias seguintes, cidade e temperatura.
- O dia de hoje usa a **condição atual** (não a "pior condição do dia"), e garoa fraca é exibida como sol+nuvem em vez de chuva.

**Imagem**
- Envio de imagem (`.jpg` / `.png` / `.gif`) para **3 espaços** do tema, com acumulação (enviar no espaço 2 não apaga o que está no 1).
- Botões **Exibir Imagem 1/2/3** para alternar as imagens na minitela.
- Recuperação do tema original.

**Agenda**
- **Janelas de horário** (início e fim): ao entrar na janela, troca a tela e define o brilho.
- Durante a janela, o app **confere o brilho a cada 60 s** e o corrige se você mudar no aparelho.
- Suporta janelas que cruzam a meia-noite (ex.: 22:00 → 06:00).

**Bandeja do sistema**
- Menu com brilho (100%, 30%, 2%, 1%, Desligado), mostrar janela e sair.
- Duplo clique no ícone abre a janela.
- Instância única: abrir o app de novo apenas traz a janela para frente.

**Tecla física**
- A tecla exclusiva do notebook alterna as telas: Notas → Monitor → Clima → Imagem 1 → Imagem 2 → Imagem 3 → Notas.

---

## Requisitos

- Windows 10/11
- Notebook Positivo com mini tela (ex.: Vision R15M)
- WebView2 Runtime (instalado por padrão no Windows 11; o instalador do app cuida disso)
- Para instalar as imagens no tema: o app oficial da Positivo **não é mais obrigatório** — o Minitela Go usa os arquivos em `%LOCALAPPDATA%\MinitelaGo\theme_gen` e os cria na primeira uso quando o app oficial está presente.

---

## Instalação

Na [página de releases](../../releases) baixe um dos arquivos:

| Arquivo | Descrição |
|---|---|
| `Minitela-Go-1.1.0-Setup.exe` | Instalador por usuário (sem precisar de administrador). Cria atalhos no Menu Iniciar e na Área de Trabalho. |
| `Minitela-Go-1.1.0-portable.zip` | Versão portátil: extraia e execute `minitela-gui.exe`. |

### Primeiros passos

1. Feche o app oficial `MiniTelaApp`.
2. Inicie o **Minitela Go**.
3. Ligue o **"Conectar na inicialização"** em *Configurações* (opcional, mas recomendado).
4. Use a tecla física do notebook para navegar entre as telas.

---

## Limites do aparelho (importante)

A tela da minitela é controlada por um tema com campos **fixos**. O que existe nele:

| Tela | O que o tema realmente tem |
|---|---|
| **Notas** | **1** campo de texto (`Reminder1`). Por isso o app tem **1 lembrete**. |
| **Clima** | Condição de **hoje + 2 dias**, cidade e faixa de temperatura. Por isso a minitela mostra 3 dias (o app mostra 5 no painel). |
| **Monitor** | Bateria, Wi-Fi e Bluetooth. **Não há campo de CPU/GPU** no tema. |

O app escreve **apenas** nos registros que o tema aceita. Escrever em registros inexistentes fazia a minitela parar de responder, por isso essa compatibilidade é verificada no `data.json` do tema.

---

## Como funciona

A mini tela é um painel HMI que aparece como **porta serial virtual (CDC)**. No Windows o dispositivo se identifica como:

```
USB\VID_0324&PID_0324  ("Dispositivo Serial USB (COMx)")
```

O protocolo é UART a **115200 baud**, 8N1, com quadros enquadrados por `AH`...`MI`:

```
[41 48] [control 2B] [cmdType 2B] [conteúdo nB] [CRC 2B] [4D 49]
```

Comandos principais:

- `HANDSHAKE (0x0080)` — estabelece a comunicação
- `SET_REGISTER (0x0090)` — escreve/lê os registros (tags) exibidos na tela
- Download/OTA — usado para enviar o tema e as imagens

### Registros usados (verificados no tema)

| Registro | Nome | Uso |
|---|---|---|
| 2 | Página atual | troca de tela (2=Notas, 3=Monitor, 4=Clima, 5/6/7=Imagens) |
| 4 / 5 | Data / Hora | relógio do aparelho |
| 7 | Backlight | brilho (0–100) |
| 65 | Animação do tema | — |
| 1082 | Battery_Percent | bateria (string com `%`) |
| 1083 | Wifi_SSID | nome da rede |
| 1085 | BT_Name | nome do dispositivo Bluetooth |
| 1090 | Reminder1 | texto do lembrete |
| 1110 / 1115 / 1120 | Weather_1/2/3_Type | ícone da condição (hoje, +1, +2) |
| 1119 / 1124 | Weather_2/3_Temp_Desc | data sob a previsão |
| 1150 | Battery_Type | ícone/tipo de bateria |
| 2005 | whatsapp_logo | logo do WhatsApp (página 1, desativada) |
| 2006 | dateHour | relógio no topo da tela |
| 2027 | city | nome da cidade |
| 2030 | currentTemp | faixa de temperatura de hoje |
| 2031 / 2032 | forecastTemp1 / 2 | faixas dos próximos dias |

---

## CLI (opcional)

O repositório também inclui uma interface de linha de comando:

```powershell
go build -o minitela.exe ./cmd/minitela-cli
```

```powershell
.\minitela.exe handshake            # testar a conexão
.\minitela.exe brightness 60        # brilho (0-100)
.\minitela.exe text "Bom dia!" -brilho 60
.\minitela.exe datetime             # enviar data/hora
.\minitela.exe page 3               # trocar de tela
.\minitela.exe get 5                # ler um registro
.\minitela.exe monitor 5            # monitorar a cada N segundos
```

A porta COM é detectada automaticamente. Para forçar, use a variável de ambiente:

```powershell
$env:MINITELA_PORT = "COM3"
```

---

## Compilando a partir do código

**App gráfico** (requer [Go](https://go.dev/) e [Wails v2](https://wails.io/)):

```powershell
cd minitela-gui
wails build              # gera build/bin/minitela-gui.exe
```

Com instalador (requer [NSIS](https://nsis.sourceforge.io/)):

```powershell
wails build -nsis -installscope user
```

**CLI:**

```powershell
go build -o minitela.exe ./cmd/minitela-cli
```

---

## Estrutura

```
minitela/              # biblioteca do protocolo (sem GUI)
  command.go           # enquadramento das mensagens (AH..MI)
  crc16.go             # CRC-16/ARC
  port.go              # porta serial + leitura de quadros
  registers.go         # registros/tags
  download.go          # envio de arquivos (OTA) para a minitela
  minitela.go          # API de alto nível
  discovery_windows.go # detecção automática da porta

cmd/minitela-cli/      # interface de linha de comando

minitela-gui/          # app Windows (Wails v2 + Go)
  app.go               # janela, monitor, Notas, Clima, Imagem, Agenda
  notes.go             # lembretes e repetição
  weather.go           # previsão do tempo (Open-Meteo)
  schedule.go          # janelas da Agenda
  assetgen.go          # geração do tema e das imagens
  tray.go              # ícone na bandeja do sistema
  keyboardhook.go      # tecla física do notebook
  main.go              # instância única
  frontend/            # interface (HTML/CSS/JS)
```

---

## Solução de problemas

**A minitela não responde / o app desconecta**
- Feche e abra o app pelo botão **Reconectar**.
- Verifique se o `MiniTelaApp` oficial está fechado (a minitela só Talk com um programa por vez).
- Troque o cabo USB.

**A imagem não mudou na minitela**
- Use os botões **Exibir Imagem N** para alternar até o espaço enviado.
- Enviar uma imagem reinicia a minitela; o app reconecta sozinho em alguns segundos.

**A bandeja não abre o menu**
- Clique direito (e duplo clique) no ícone. Se estiver travada, execute o app novamente pelo atalho.

---

## Licença

MIT
