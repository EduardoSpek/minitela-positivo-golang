package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// GimFile is a theme GIF that maps to a mini-screen image page.
// pageNum is the data.json pageList index (0-based) whose page we replace.
type GifFile struct {
	Name    string // filename inside file.zip
	PageNum int    // mini-screen image page number (1..3 on the Imagem screen)
}

// themeGifs maps the "Imagem" image pages to the GIF filename inside file.zip.
// Confirmed from data.json: Gif1(page 4)=1i1h..., Gif2(page 5)=1h1k..., Gif3(page 6)=1h1m...
var themeGifs = []GifFile{
	{Name: "1i1h1e37393671471.gif", PageNum: 1}, // Gif1 (PageImagem=5 -> Gif1)
	{Name: "1h1k1e37393671464.gif", PageNum: 2}, // Gif2
	{Name: "1h1m1e37393671466.gif", PageNum: 3}, // Gif3
}

// ideUtilsBase returns the base folder of the installed Positivo IDE_utils_pt
// that holds Gen/ and Zip/file.zip. It scans WindowsApps for any installed
// PositivoMinitela_* version (the Store updates the folder name on every app
// update, so a fixed version string breaks), keeping the last known path as a
// fallback candidate.
func ideUtilsBase() string {
	candidates := []string{}
	entries, err := os.ReadDir(`C:\Program Files\WindowsApps`)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasPrefix(name, "PositivoInformticaS.A.PositivoMinitela_") {
				continue
			}
			candidates = append(candidates, filepath.Join(`C:\Program Files\WindowsApps`, name,
				`MiniTelaApp`, `assets`, `minipanel`, `resources`, `IDE_utils_pt`))
		}
	}
	// Last resort: the last known fixed path (kept for reference).
	candidates = append(candidates,
		`C:\Program Files\WindowsApps\PositivoInformticaS.A.PositivoMinitela_1.0.43.0_x64__6yhrh9dmgepzj\MiniTelaApp\assets\minipanel\resources\IDE_utils_pt`,
	)
	for _, p := range candidates {
		if _, err := os.Stat(filepath.Join(p, "Gen", "AHMISimGenDemo_og.exe")); err == nil {
			return p
		}
	}
	return ""
}

// workArea returns a writable scratch folder used to regenerate the theme.
func workArea() string {
	if runtime.GOOS == "windows" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = os.TempDir()
		}
		return filepath.Join(base, "MinitelaGo", "theme_gen")
	}
	return filepath.Join(os.TempDir(), "MinitelaGo", "theme_gen")
}

// prepareWorkArea copies Gen/ and file.zip from the install into a writable
// area so the generator exe can run (the WindowsApps install is read-only).
// If the install is gone (uninstalled/updated) but the work area already has
// Gen/ and file.zip from a previous run, it proceeds with those copies.
func prepareWorkArea() (string, error) {
	src := ideUtilsBase()
	work := workArea()
	genDst := filepath.Join(work, "Gen")
	genExe := filepath.Join(genDst, "AHMISimGenDemo_og.exe")
	zipDst := filepath.Join(work, "Zip", "file.zip")
	if src == "" {
		if _, err := os.Stat(genExe); err != nil {
			return "", fmt.Errorf("instalação IDE_utils_pt não localizada")
		}
		if _, err := os.Stat(zipDst); err != nil {
			return "", fmt.Errorf("instalação IDE_utils_pt não localizada")
		}
		return work, nil
	}
	if err := os.MkdirAll(filepath.Join(work, "Gen"), 0o755); err != nil {
		return "", err
	}
	// copy Gen/ recursively if missing or stale
	genSrc := filepath.Join(src, "Gen")
	if _, err := os.Stat(genExe); err != nil {
		if err := copyDir(genSrc, genDst); err != nil {
			return "", err
		}
	}
	// copy file.zip if missing
	zipSrc := filepath.Join(src, "Zip", "file.zip")
	if _, err := os.Stat(zipDst); err != nil {
		if err := os.MkdirAll(filepath.Dir(zipDst), 0o755); err != nil {
			return "", err
		}
		if err := copyFile(zipSrc, zipDst); err != nil {
			return "", err
		}
	}
	return work, nil
}

// persistThemeBase promotes a generated theme zip to be the base for the next
// upload, so previously embedded images accumulate instead of reverting to
// the factory theme on every send. Call only after the theme was flashed to
// the device successfully.
func persistThemeBase(work, generatedZip string) error {
	base := filepath.Join(work, "Zip", "file.zip")
	if err := os.MkdirAll(filepath.Dir(base), 0o755); err != nil {
		return err
	}
	return copyFile(generatedZip, base)
}

// convertImageToGif turns raw image bytes (jpg/png/...) into a 192x192
// optimized GIF on disk, replicating the official app's gifUtils output.
func convertImageToGif(imgBytes []byte, gifPath, python string) error {
	src := gifPath + ".src"
	if err := os.WriteFile(src, imgBytes, 0o644); err != nil {
		return err
	}
	defer os.Remove(src)

	script := `
import sys
from PIL import Image
im = Image.open(sys.argv[1]).convert('RGB')
W = H = 192
im = im.resize((W, H), Image.LANCZOS)
im = im.quantize(colors=256, method=Image.MEDIANCUT, dither=Image.FLOYDSTEINBERG)
im = im.convert('P')
im.save(sys.argv[2], 'GIF', save_all=True, loop=0)
`
	args := []string{"-c", script, src, gifPath}
	cmd := exec.Command(python, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("conversão de imagem: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// embedGifInZip replaces the theme gif inside a copy of file.zip using Python
// (Go's archive/zip rejects this zip's CRC on data.json). Returns the path to
// the generated zip.
func embedGifInZip(work, python, gifName, gifPath string) (string, error) {
	zipSrc := filepath.Join(work, "Zip", "file.zip")
	zipDst := filepath.Join(work, "Zip", "file_generated.zip")

	script := `
import sys, zipfile, shutil
src, dst, gifname, gifpath = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
gif = open(gifpath, 'rb').read()
with zipfile.ZipFile(src, 'r') as zin:
    with zipfile.ZipFile(dst, 'w', zipfile.ZIP_DEFLATED) as zout:
        for info in zin.infolist():
            data = gif if info.filename == gifname else zin.read(info.filename)
            zout.writestr(info, data)
`
	cmd := exec.Command(python, "-c", script, zipSrc, zipDst, gifName, gifPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("embutir gif no zip: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return zipDst, nil
}

// generateThemeAcf runs AHMISimGenDemo_og.exe on the modified file.zip and
// returns the generated Texture.acf bytes (Stock theme + the embedded GIF).
func generateThemeAcf(work, zipPath string) ([]byte, error) {
	genDir := filepath.Join(work, "Gen")
	exe := filepath.Join(genDir, "AHMISimGenDemo_og.exe")
	acfOut := filepath.Join(work, "ACF")
	if err := os.MkdirAll(acfOut, 0o755); err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, "-f", zipPath, "-m", "2", "-c", "0", "-e", "0", "-d", "1", "-o", acfOut)
	cmd.Dir = genDir
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	stdin, _ := cmd.StdinPipe()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// the exe waits for a keypress ("13") before fully writing output.
	go func() {
		stdin.Write([]byte("13"))
		stdin.Close()
	}()
	if err := cmd.Wait(); err != nil {
		msg := strings.TrimSpace(outBuf.String())
		if m := strings.TrimSpace(errBuf.String()); m != "" {
			msg = m
		}
		return nil, fmt.Errorf("geração ACF: %w: %s", err, msg)
	}
	// the exe writes Texture.acf
	acf := filepath.Join(acfOut, "Texture.acf")
	data, err := os.ReadFile(acf)
	if err != nil {
		return nil, fmt.Errorf("ler Texture.acf: %w", err)
	}
	return data, nil
}

// copyDir copies src/* recursively into dst.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		dest := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		return copyFile(p, dest)
	})
}

// copyFile copies a single file.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	out.Close()
	return err
}
