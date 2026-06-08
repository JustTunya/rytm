package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"rytm/internal/ipc"
	"rytm/internal/ui"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func ensureDaemonRunning() (net.Conn, error) {
	conn, err := net.Dial("unix", ipc.SocketPath)
	if err == nil {
		return conn, nil
	}

	exePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("could not resolve client path: %w", err)
	}
	
	daemonName := "rytmd"
	if runtime.GOOS == "windows" {
		daemonName += ".exe"
	}
	daemonPath := filepath.Join(filepath.Dir(exePath), daemonName)

	cmd := exec.Command(daemonPath)
	cmd.Stdout = nil
	cmd.Stderr = nil
	
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start daemon at %s: %w", daemonPath, err)
	}

	for i := 0; i < 10; i++ { 
		time.Sleep(time.Millisecond * 200)
		conn, err = net.Dial("unix", ipc.SocketPath)
		if err == nil {
			return conn, nil
		}
	}

	return nil, fmt.Errorf("daemon started but socket remains unreachable at %s", ipc.SocketPath)
}

func main() {
	conn, err := ensureDaemonRunning()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Startup Error: %v\n", err)
		os.Exit(1)
	}
	
	conn.Close()

	model := ui.InitialModel()
	p := tea.NewProgram(&model)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Fatal UI error: %v\n", err)
		os.Exit(1)
	}
}