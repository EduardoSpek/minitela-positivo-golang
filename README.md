# minitela-go

Controlador da **mini tela** dos notebooks Positivo (1.54" IPS 240×240, abaixo do teclado), escrito em Go. Este projeto foi feito por engenharia reversa do app oficial `MiniTelaApp` (Electron + protocolo UART) e do projeto de referência `open-minitela-R15M`.

## Como funciona

A mini tela é um painel HMI que aparece como **porta serial virtual (CDC)** no sistema. No Windows, o dispositivo se identifica como:

```
USB\VID_0324&PID_0324  ("Dispositivo Serial USB (COMx)")
```

O protocolo é UART a **115200 baud**, 8N1, com quadros enquadrados por `AH`...`MI`:

```
[41 48] [control 2B] [cmdType 2B] [conteúdo nB] [CRC 2B] [4D 49]
```

O bit 0x15 do *control* habilita CRC. O app oficial envia com CRC desabilitado. Os comandos relevantes são:

- `HANDSHAKE (0x0080)` → estabelece comunicação
- `SET_REGISTER (0x0090)` → escreve/lê os registros (tags) que a tela exibe

### Registros (tags) importantes

| Registro | Nome | Tipo |
|----------|------|------|
| 1082 | Bateria % | numérico |
| 1080 / 1081 | CPU / GPU % | numérico |
| 1083 | WiFi SSID | string |
| 1084 | Qualidade WiFi | numérico |
| 1100 | Nome da mídia | string |
| 1150 | Tipo de bateria (barra) | numérico |
| 2003 | Tocar/Pausar mídia | numérico |

**Tags de sistema:** `Date=4`, `Time=5`, `Backlight(背光)=7`, `Page(当前页面序号)=2`, `CPU0/1 versão=12/13`.

## Compilar

```powershell
go build -o minitela.exe ./cmd/minitela-cli
```

## Uso

> **Importante:** a mini tela só pode ser usada por um programa por vez. Feche o app oficial
> `MiniTelaApp` antes de usar este controlador (ou rode-o após fechar o nosso).

```powershell
# Testar a conexão (handshake)
.\minitela.exe handshake

# Controlar o brilho (0-100)
.\minitela.exe brightness 60

# Exibir um texto ASCII (máx. 100 caracteres)
.\minitela.exe text "Bom dia!" -brilho 60

# Enviar data/hora atual
.\minitela.exe datetime

# Trocar a página exibida
.\minitela.exe page 3

# Ler um registro (ex.: hora)
.\minitela.exe get 5

# Monitorar CPU/bateria/WiFi (atualiza na tela a cada N segundos)
.\minitela.exe monitor 5
```

### Detecção da porta

No Windows a porta COM é detectada automaticamente via registro USB `VID_0324`. Em outros sistemas
(ou se a detecção falhar), defina a variável de ambiente:

```powershell
$env:MINITELA_PORT = "COM3"
```

## Estrutura

```
minitela/
  command.go        # enquadramento do protocolo (AH..MI)
  crc16.go          # CRC-16/ARC (para o modo com CRC)
  port.go           # porta serial + leitura de quadros
  registers.go      # definição de registros/tags
  minitela.go       # API de alto nível (texto, brilho, data/hora, tags)
  discovery_windows.go  # detecção automática da porta no Windows
cmd/minitela-cli/
  main.go           # interface de linha de comando
```

## Notas

- O texto exibido no modo `text` é **ASCII puro** (o firmware não suporta acentos/UTF-8).
- A sequência de escrita de texto reproduz o `open-minitela-R15M`: mudar para página 2, limpar
  o buffer, renderizar o texto e (opcionalmente) ajustar o brilho.
- Ao encerrar nosso controle, quando o app oficial não está rodando, a tela pode desligar após
  certo tempo; basta redefinir o brilho (ex.: `minitela.exe brightness 60`).

## Licença

MIT
