package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	installerVersion = "1.0.0"
	fakescriptTarURL = "https://github.com/ChaiMaii/fakescript/archive/refs/heads/main.tar.gz"
	fkspmTarURL      = "https://github.com/ChaiMaii/fkspm/archive/refs/heads/main.tar.gz"
)

// ANSI styles
const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	dim    = "\033[2m"
	pink   = "\033[38;2;255;175;243m"
	cyan   = "\033[38;2;126;232;250m"
	yellow = "\033[38;2;255;239;77m"
	green  = "\033[38;2;134;239;172m"
	red    = "\033[38;2;255;107;107m"
)

func defaultInstallDir() string {
	if runtime.GOOS == "windows" {
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData != "" {
			return filepath.Join(localAppData, "FakeScript", "bin")
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".fakescript", "bin")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin")
}

func printBanner() {
	fmt.Println()
	fmt.Printf("%s%s  ╔══════════════════════════════════════════════════════╗%s\n", bold, pink, reset)
	fmt.Printf("%s%s  ║%s    /\\      %sFakeScript + fkspm Visual Installer%s    %s%s║%s\n", bold, pink, reset, bold+cyan, reset, bold, pink, reset)
	fmt.Printf("%s%s  ║%s   /fs\\     %sZero-Symbol AOT Compiler & Pkg Manager%s  %s%s║%s\n", bold, pink, reset, dim, reset, bold, pink, reset)
	fmt.Printf("%s%s  ║%s  /____\\    %sv%s (%s/%s)%s                      %s%s║%s\n", bold, pink, reset, yellow, installerVersion, runtime.GOOS, runtime.GOARCH, reset, bold, pink, reset)
	fmt.Printf("%s%s  ╚══════════════════════════════════════════════════════╝%s\n", bold, pink, reset)
	fmt.Println()
}

func renderProgress(step, total int, label string) {
	width := 28
	filled := (step * width) / total
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	pct := (step * 100) / total
	fmt.Printf("  %s[%s]%s %s%3d%%%s  %s\n", pink, bar, reset, bold, pct, reset, label)
}

func promptConfirm(r *bufio.Reader, question string, defaultYes bool) bool {
	hint := "[Y/n]"
	if !defaultYes {
		hint = "[y/N]"
	}
	fmt.Printf("  %s?%s %s %s%s%s ", cyan+bold, reset, question, dim, hint, reset)
	line, err := r.ReadString('\n')
	if err != nil {
		return defaultYes
	}
	ans := strings.TrimSpace(strings.ToLower(line))
	if ans == "" {
		return defaultYes
	}
	return ans == "y" || ans == "yes"
}

func promptString(r *bufio.Reader, question, def string) string {
	fmt.Printf("  %s?%s %s %s[%s]%s ", cyan+bold, reset, question, dim, def, reset)
	line, err := r.ReadString('\n')
	if err != nil {
		return def
	}
	ans := strings.TrimSpace(line)
	if ans == "" {
		return def
	}
	return ans
}

func isTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func downloadAndExtract(url, destDir string) error {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s returned HTTP %d", url, resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	defer gz.Close()

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}
	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return err
	}

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		clean := filepath.ToSlash(filepath.Clean(hdr.Name))
		parts := strings.SplitN(clean, "/", 2)
		if len(parts) < 2 || parts[1] == "" {
			continue
		}
		rel := parts[1]
		target := filepath.Join(absDest, filepath.FromSlash(rel))
		if !strings.HasPrefix(target, absDest+string(os.PathSeparator)) && target != absDest {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			_ = os.MkdirAll(target, 0755)
		case tar.TypeReg:
			_ = os.MkdirAll(filepath.Dir(target), 0755)
			out, err := os.Create(target)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			out.Close()
			if copyErr != nil {
				return copyErr
			}
		}
	}
	return nil
}

func buildGoBinary(srcDir, targetArg, outPath string) error {
	cmd := exec.Command("go", "build", "-o", outPath, targetArg)
	cmd.Dir = srcDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go build failed: %w\n%s", err, string(out))
	}
	return nil
}

func addToPath(binDir string) (bool, string) {
	currentPath := os.Getenv("PATH")
	for _, p := range filepath.SplitList(currentPath) {
		if strings.EqualFold(filepath.Clean(p), filepath.Clean(binDir)) {
			return false, "already in PATH"
		}
	}

	if runtime.GOOS == "windows" {
		psScript := fmt.Sprintf(
			`$p = [Environment]::GetEnvironmentVariable('Path', 'User'); if ($p -notlike '*%s*') { [Environment]::SetEnvironmentVariable('Path', ($p.TrimEnd(';') + ';%s'), 'User') }`,
			binDir, binDir,
		)
		cmd := exec.Command("powershell", "-NoProfile", "-Command", psScript)
		if err := cmd.Run(); err != nil {
			return false, "add manually: " + binDir
		}
		return true, "added to Windows User PATH (restart terminal to take effect)"
	}

	home, _ := os.UserHomeDir()
	exportLine := fmt.Sprintf("\n# FakeScript + fkspm\nexport PATH=\"%s:$PATH\"\n", binDir)
	updated := []string{}
	for _, rc := range []string{".bashrc", ".zshrc", ".profile"} {
		rcPath := filepath.Join(home, rc)
		if data, err := os.ReadFile(rcPath); err == nil {
			if !strings.Contains(string(data), binDir) {
				f, err := os.OpenFile(rcPath, os.O_APPEND|os.O_WRONLY, 0644)
				if err == nil {
					_, _ = f.WriteString(exportLine)
					f.Close()
					updated = append(updated, "~/"+rc)
				}
			}
		}
	}
	if len(updated) == 0 {
		return false, fmt.Sprintf("add `export PATH=\"%s:$PATH\"` to your shell profile", binDir)
	}
	return true, "updated " + strings.Join(updated, ", ")
}

func main() {
	yesFlag := flag.Bool("yes", false, "Non-interactive install with defaults")
	shortYes := flag.Bool("y", false, "Non-interactive install with defaults")
	dirFlag := flag.String("dir", defaultInstallDir(), "Install directory for fksc and fkspm")
	noPathFlag := flag.Bool("no-path", false, "Do not modify PATH")
	uninstallFlag := flag.Bool("uninstall", false, "Remove fksc and fkspm from install directory")
	flag.Parse()

	printBanner()

	installDir := *dirFlag
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}

	if *uninstallFlag {
		for _, bin := range []string{"fksc" + ext, "fkspm" + ext} {
			p := filepath.Join(installDir, bin)
			if err := os.Remove(p); err == nil {
				fmt.Printf("  %s✓%s Removed %s\n", green, reset, p)
			}
		}
		fmt.Printf("\n  %sUninstall complete.%s\n\n", bold, reset)
		return
	}

	// Verify Go is available
	goVerOut, err := exec.Command("go", "version").CombinedOutput()
	if err != nil {
		fmt.Printf("  %s✗ Go 1.22+ is required to install FakeScript & fkspm.%s\n", red+bold, reset)
		fmt.Printf("    Install Go from https://go.dev/dl/ and re-run this installer.\n\n")
		os.Exit(1)
	}
	fmt.Printf("  %s✓%s Detected %s\n\n", green, reset, strings.TrimSpace(string(goVerOut)))

	installFksc := true
	installFkspm := true
	updatePath := !*noPathFlag

	nonInteractive := *yesFlag || *shortYes || !isTTY()
	if !nonInteractive {
		r := bufio.NewReader(os.Stdin)
		installFksc = promptConfirm(r, "Install fksc (FakeScript Compiler)?", true)
		installFkspm = promptConfirm(r, "Install fkspm (FakeScript Package Manager)?", true)
		installDir = promptString(r, "Install directory:", installDir)
		updatePath = promptConfirm(r, "Add install directory to PATH?", true)
		fmt.Println()
	}

	if !installFksc && !installFkspm {
		fmt.Printf("  %sNothing selected to install. Exiting.%s\n\n", yellow, reset)
		return
	}

	fmt.Printf("  %sInstall Plan:%s\n", bold, reset)
	if installFksc {
		fmt.Printf("    %s•%s fksc  → %s\n", pink, reset, filepath.Join(installDir, "fksc"+ext))
	}
	if installFkspm {
		fmt.Printf("    %s•%s fkspm → %s\n", cyan, reset, filepath.Join(installDir, "fkspm"+ext))
	}
	fmt.Println()

	totalSteps := 2
	if installFksc {
		totalSteps += 2
	}
	if installFkspm {
		totalSteps += 2
	}
	step := 0

	if err := os.MkdirAll(installDir, 0755); err != nil {
		fmt.Printf("  %s✗ Failed to create %s: %v%s\n", red, installDir, err, reset)
		os.Exit(1)
	}

	tmpDir, err := os.MkdirTemp("", "fakescript-installer-*")
	if err != nil {
		fmt.Printf("  %s✗ Temp dir error: %v%s\n", red, err, reset)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	step++
	renderProgress(step, totalSteps, "Prepared workspace")

	if installFksc {
		fksSrc := filepath.Join(tmpDir, "fakescript")
		step++
		renderProgress(step, totalSteps, "Downloading FakeScript compiler (fksc)...")
		if err := downloadAndExtract(fakescriptTarURL, fksSrc); err != nil {
			fmt.Printf("  %s✗ %v%s\n", red, err, reset)
			os.Exit(1)
		}
		step++
		renderProgress(step, totalSteps, "Building native fksc binary...")
		if err := buildGoBinary(fksSrc, "fksc.go", filepath.Join(installDir, "fksc"+ext)); err != nil {
			fmt.Printf("  %s✗ %v%s\n", red, err, reset)
			os.Exit(1)
		}
	}

	if installFkspm {
		pmSrc := filepath.Join(tmpDir, "fkspm")
		step++
		renderProgress(step, totalSteps, "Downloading FakeScript Package Manager (fkspm)...")
		if err := downloadAndExtract(fkspmTarURL, pmSrc); err != nil {
			fmt.Printf("  %s✗ %v%s\n", red, err, reset)
			os.Exit(1)
		}
		step++
		renderProgress(step, totalSteps, "Building native fkspm binary...")
		if err := buildGoBinary(pmSrc, ".", filepath.Join(installDir, "fkspm"+ext)); err != nil {
			fmt.Printf("  %s✗ %v%s\n", red, err, reset)
			os.Exit(1)
		}
	}

	pathMsg := "skipped (--no-path)"
	if updatePath {
		_, pathMsg = addToPath(installDir)
	}
	step++
	renderProgress(step, totalSteps, "Finalized PATH ("+pathMsg+")")

	fmt.Println()
	fmt.Printf("  %s%s✓ Installation Complete!%s\n\n", bold, green, reset)
	if installFksc {
		if out, err := exec.Command(filepath.Join(installDir, "fksc"+ext), "version").CombinedOutput(); err == nil {
			fmt.Printf("    %s%s%s\n", cyan, strings.TrimSpace(string(out)), reset)
		}
	}
	if installFkspm {
		if out, err := exec.Command(filepath.Join(installDir, "fkspm"+ext), "version").CombinedOutput(); err == nil {
			fmt.Printf("    %s%s%s\n", pink, strings.TrimSpace(string(out)), reset)
		}
	}
	fmt.Printf("\n  %sGet started:%s\n", bold, reset)
	fmt.Printf("    fkspm init myapp\n")
	fmt.Printf("    cd myapp\n")
	fmt.Printf("    fksc run main.fks\n\n")
}
