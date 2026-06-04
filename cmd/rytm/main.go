package main

import (
	"fmt"
	"os"

	"rytm/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	p := tea.NewProgram(ui.InitialModel())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Fatal UI error: %v\n", err)
		os.Exit(1)
	}
}