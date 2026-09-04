package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"minitela/minitela"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "text":
		cmdText(args)
	case "brightness", "brilho", "backlight":
		cmdBrightness(args)
	case "datetime", "date", "time":
		cmdDateTime(args)
	case "monitor", "mon":
		cmdMonitor(args)
	case "page":
		cmdPage(args)
	case "get":
		cmdGet(args)
	case "setnum":
		cmdSetNum(args)
	case "setstr":
		cmdSetStr(args)
	case "handshake", "ping":
		cmdHandshake(args)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "comando desconhecido: %s\n\n", cmd)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `minitela - controla a mini tela Positivo (USB serial)

Uso:
  minitela text "<texto ASCII (max 100 chars)>" [-brilho 0-100]
  minitela brightness <0-100>
  minitela datetime                 # envia data/hora atuais
  minitela monitor [intervalo_s]    # monitora CPU/bateria/WiFi (5s padrão)
  minitela page <1-N>               # troca a página exibida
  minitela get <regId>              # lê um registro numérico
  minitela handshake                # testa a conexão com o firmware
  minitela help

Se a porta não for detectada automaticamente, defina a variável MINITELA_PORT
com o nome da porta (ex.: COM3).`)
}

// connect opens a configured connection, auto-detecting on Windows.
func connect() *minitela.Client {
	name := os.Getenv("MINITELA_PORT")
	var c *minitela.Client
	var err error
	if name != "" {
		c, err = minitela.ConnectPort(name)
	} else {
		c, err = minitela.Connect()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "erro ao conectar: %v\n", err)
		os.Exit(1)
	}
	return c
}

func cmdText(args []string) {
	brightness := -1
	var rest []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-brilho", "--brilho":
			if i+1 < len(args) {
				if v, err := strconv.Atoi(args[i+1]); err == nil {
					brightness = v
				}
				i++
			}
		default:
			rest = append(rest, args[i])
		}
	}

	if len(rest) < 1 || rest[0] == "" {
		fmt.Fprintln(os.Stderr, "uso: minitela text \"<texto>\" [-brilho 0-100]")
		os.Exit(1)
	}
	text := strings.Join(rest, " ")

	c := connect()
	defer c.Close()

	if err := c.WriteTextWithBrightness(text, brightness); err != nil {
		fmt.Fprintf(os.Stderr, "erro: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("texto enviado: %q\n", text)
}

func cmdBrightness(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "uso: minitela brightness <0-100>")
		os.Exit(1)
	}
	v, err := strconv.Atoi(args[0])
	if err != nil || v < 0 || v > 100 {
		fmt.Fprintf(os.Stderr, "brilho deve ser um inteiro entre 0 e 100 (valor inválido: %q)\n", args[0])
		os.Exit(1)
	}
	c := connect()
	defer c.Close()
	if err := c.SetBacklight(v); err != nil {
		fmt.Fprintf(os.Stderr, "erro: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("brilho ajustado para %d%%\n", v)
}

func cmdDateTime(args []string) {
	c := connect()
	defer c.Close()
	if err := c.SetDateTime(time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "erro: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("data/hora enviadas")
}

func cmdPage(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "uso: minitela page <1-N>")
		os.Exit(1)
	}
	v, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "página inválida: %q\n", args[0])
		os.Exit(1)
	}
	c := connect()
	defer c.Close()
	if err := c.SetPage(int32(v)); err != nil {
		fmt.Fprintf(os.Stderr, "erro: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("página alterada para %d\n", v)
}

func cmdGet(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "uso: minitela get <regId>")
		os.Exit(1)
	}
	id, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "regId inválido: %q\n", args[0])
		os.Exit(1)
	}
	c := connect()
	defer c.Close()
	res, err := c.GetNumTags([]uint16{uint16(id)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "erro: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("registro %d = %d\n", id, res[uint16(id)])
}

func cmdSetNum(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "uso: minitela setnum <reg> <valor>")
		os.Exit(1)
	}
	id, err1 := strconv.Atoi(args[0])
	val, err2 := strconv.Atoi(args[1])
	if err1 != nil || err2 != nil {
		fmt.Fprintln(os.Stderr, "reg e valor devem ser inteiros")
		os.Exit(1)
	}
	c := connect()
	defer c.Close()
	if err := c.SetNumTag(uint16(id), int32(val)); err != nil {
		fmt.Fprintf(os.Stderr, "erro: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("registro %d = %d (numerico)\n", id, val)
}

func cmdSetStr(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "uso: minitela setstr <reg> <texto>")
		os.Exit(1)
	}
	id, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "reg deve ser inteiro")
		os.Exit(1)
	}
	text := strings.Join(args[1:], " ")
	c := connect()
	defer c.Close()
	if err := c.SetStringTag(uint16(id), text); err != nil {
		fmt.Fprintf(os.Stderr, "erro: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("registro %d = %q (string)\n", id, text)
}

func cmdHandshake(args []string) {
	c := connect()
	defer c.Close()
	maxPacket, err := c.Handshake()
	if err != nil {
		fmt.Fprintf(os.Stderr, "handshake falhou: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("handshake OK (maxPacketLength=%d)\n", maxPacket)
}

func cmdMonitor(args []string) {
	interval := 5 * time.Second
	if len(args) >= 1 {
		if s, err := strconv.Atoi(args[0]); err == nil && s > 0 {
			interval = time.Duration(s) * time.Second
		}
	}
	c := connect()
	defer c.Close()

	// First, try to render live data onto the text page so the user sees it.
	_ = c.WriteTextWithBrightness("Monitor:", 0)

	fmt.Printf("monitorando a cada %v (Ctrl+C para parar)\n", interval)
	for {
		txt := gatherSystemText()
		_ = c.SetStringTag(minitela.RegReminder1Text, txt[:min(100, len(txt))])
		fmt.Println(txt)
		time.Sleep(interval)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// gatherSystemText collects CPU / battery / WiFi info using PowerShell.
func gatherSystemText() string {
	out := &strings.Builder{}
	out.WriteString(fmt.Sprintf("CPU:%s%% ", getCPU()))
	out.WriteString(fmt.Sprintf("BAT:%s%% ", getBattery()))
	ssid, q := getWiFi()
	out.WriteString(fmt.Sprintf("WiFi:%s(%s)", ssid, q))
	return out.String()
}

func pow(s string) string { return strings.TrimSpace(s) }

// runPS executes a PowerShell expression and returns its trimmed output.
func runPS(expr string) string {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", expr)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func getCPU() string {
	return pow(runPS("(Get-CimInstance Win32_Processor).LoadPercentage"))
}

func getBattery() string {
	return pow(runPS("(Get-CimInstance Win32_Battery).EstimatedChargeRemaining"))
}

func getWiFi() (string, string) {
	cmd := exec.Command("netsh", "wlan", "show", "interfaces")
	out, err := cmd.Output()
	if err != nil {
		return "-", "-"
	}
	var ssid, sig string
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		lower := strings.ToLower(l)
		if strings.Contains(lower, ":") && (strings.HasPrefix(lower, "ssid") || strings.Contains(lower, "ssid ")) && !strings.Contains(lower, "bssid") {
			parts := strings.SplitN(l, ":", 2)
			if len(parts) > 1 {
				ssid = strings.TrimSpace(parts[1])
			}
		}
		if strings.Contains(lower, "sinal") || strings.Contains(lower, "signal") {
			parts := strings.SplitN(l, ":", 2)
			if len(parts) > 1 {
				sig = strings.TrimSpace(strings.TrimSuffix(parts[1], "%"))
			}
		}
	}
	return ssid, sig
}
