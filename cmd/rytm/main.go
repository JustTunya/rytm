package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"rytm/internal/ipc"
	"rytm/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
)

func ensureDaemonRunning() (net.Conn, error) {
	// 1. Try to connect to existing daemon socket
	conn, err := net.Dial("unix", ipc.SocketPath)
	if err == nil {
		return conn, nil
	}

	// 2. Resolve exact path to the daemon executable dynamically
	exePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("could not resolve client path: %w", err)
	}
	
	daemonName := "rytmd"
	if runtime.GOOS == "windows" {
		daemonName += ".exe"
	}
	daemonPath := filepath.Join(filepath.Dir(exePath), daemonName)

	// 3. Spawn the daemon detached in the background
	cmd := exec.Command(daemonPath)
	cmd.Stdout = nil
	cmd.Stderr = nil
	
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start daemon at %s: %w", daemonPath, err)
	}

	// 4. Retry connection loop to give the daemon time to bind the socket
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
	// Ensure the background engine is running before rendering the TUI
	conn, err := ensureDaemonRunning()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Startup Error: %v\n", err)
		os.Exit(1)
	}
	
	// Close this initial probe connection; the UI loop manages its own connections
	conn.Close()

	// Launch the Bubble Tea interface
	model := ui.InitialModel()
	p := tea.NewProgram(&model)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Fatal UI error: %v\n", err)
		os.Exit(1)
	}
}