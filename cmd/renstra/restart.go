package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"renstra/internal/cli"
	"renstra/internal/config"
)

const pidFileName = "renstra.pid"

func pidFilePath(home string) string {
	return filepath.Join(home, pidFileName)
}

func writePIDFile(home string) (func(), error) {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return func() {}, err
	}
	path := pidFilePath(home)
	pid := os.Getpid()
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		return func() {}, err
	}
	return func() {
		raw, err := os.ReadFile(path)
		if err != nil {
			return
		}
		if cur, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && cur == pid {
			_ = os.Remove(path)
		}
	}, nil
}

func readLivePID(home string) (int, bool) {
	path := pidFilePath(home)
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		_ = os.Remove(path)
		return 0, false
	}
	if pid == os.Getpid() || !isProcessAlive(pid) {
		_ = os.Remove(path)
		return 0, false
	}
	return pid, true
}

func restartCmd(args []string, out io.Writer) error {
	if out == nil {
		out = os.Stdout
	}
	w := cli.Writer(out)
	startIfMissing := false
	for _, a := range args {
		if a == "--start" || a == "-s" {
			startIfMissing = true
		}
	}

	// 1. Check systemd service on Linux first.
	if runtime.GOOS == "linux" {
		if handled, err := restartSystemdService(w); handled {
			return err
		}
	}

	// 2. Check standalone `renstra serve` via PID file in $RENSTRA_HOME.
	home := config.Home()
	if pid, alive := readLivePID(home); alive {
		fmt.Fprintf(w, "%s🔄 Menghentikan renstra serve (PID %d)...%s\n", cli.ColorYellow, pid, cli.ColorReset)
		if err := stopProcess(pid, 5*time.Second); err != nil {
			return fmt.Errorf("gagal menghentikan proses %d: %w", pid, err)
		}
		_ = os.Remove(pidFilePath(home))
		newPID, logPath, err := startServeDetached(home)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%s✅ Server renstra berhasil di-restart%s (PID baru: %s%d%s, log: %s%s%s)\n",
			cli.ColorGreen, cli.ColorReset, cli.ColorCyan, newPID, cli.ColorReset, cli.ColorDim, logPath, cli.ColorReset)
		return nil
	}

	if startIfMissing {
		newPID, logPath, err := startServeDetached(home)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%s🚀 Server renstra dimulai di background%s (PID: %s%d%s, log: %s%s%s)\n",
			cli.ColorGreen, cli.ColorReset, cli.ColorCyan, newPID, cli.ColorReset, cli.ColorDim, logPath, cli.ColorReset)
		return nil
	}

	fmt.Fprintf(w, "%sℹ️ Tidak ada service atau server renstra serve yang sedang berjalan.%s\n", cli.ColorYellow, cli.ColorReset)
	fmt.Fprintf(w, "%sTips: jalankan `renstra serve` atau `renstra restart --start` untuk menyalakannya.%s\n", cli.ColorDim, cli.ColorReset)
	return nil
}

func restartSystemdService(w io.Writer) (bool, error) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false, nil
	}
	// User-level systemd unit
	if exec.Command("systemctl", "--user", "cat", "renstra").Run() == nil {
		fmt.Fprintf(w, "%s🔄 Me-restart systemd user service renstra...%s\n", cli.ColorCyan, cli.ColorReset)
		cmd := exec.Command("systemctl", "--user", "restart", "renstra")
		if out, err := cmd.CombinedOutput(); err != nil {
			return true, fmt.Errorf("systemctl --user restart renstra: %w: %s", err, strings.TrimSpace(string(out)))
		}
		fmt.Fprintf(w, "%s✅ Service renstra (--user) berhasil di-restart.%s\n", cli.ColorGreen, cli.ColorReset)
		return true, nil
	}
	// System-level systemd unit
	if exec.Command("systemctl", "cat", "renstra").Run() == nil || exec.Command("systemctl", "is-active", "--quiet", "renstra").Run() == nil {
		fmt.Fprintf(w, "%s🔄 Me-restart systemd service renstra...%s\n", cli.ColorCyan, cli.ColorReset)
		cmd := exec.Command("systemctl", "restart", "renstra")
		if os.Geteuid() != 0 {
			if _, serr := exec.LookPath("sudo"); serr == nil {
				cmd = exec.Command("sudo", "systemctl", "restart", "renstra")
				cmd.Stdin = os.Stdin
			}
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			return true, fmt.Errorf("systemctl restart renstra: %w: %s", err, strings.TrimSpace(string(out)))
		}
		fmt.Fprintf(w, "%s✅ Service renstra berhasil di-restart.%s\n", cli.ColorGreen, cli.ColorReset)
		return true, nil
	}
	return false, nil
}

func startServeDetached(home string) (int, string, error) {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return 0, "", err
	}
	exe, err := os.Executable()
	if err != nil {
		return 0, "", err
	}
	logPath := filepath.Join(home, "renstra.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, "", err
	}
	defer logFile.Close()

	cmd := exec.Command(exe, "serve")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = detachedSysProcAttr()
	if err := cmd.Start(); err != nil {
		return 0, "", fmt.Errorf("gagal menjalankan `renstra serve`: %w", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, logPath, nil
}
