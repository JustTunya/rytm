package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	// Minimalist styling components matching a crisp terminal look
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Bold(true)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))
	borderStyle = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#333333")).Padding(1, 2)
)

func (m Model) View() string {
	var doc strings.Builder

	doc.WriteString(accentStyle.Render("RYTM // Media Ingestion Engine"));doc.WriteString("\n\n")

	switch m.State {
	case StateInput:
		doc.WriteString("Search Index / Ingest Stream:\n")
		doc.WriteString(m.TextInput.View());doc.WriteString("\n\n")
		doc.WriteString(dimStyle.Render("Press [q] or [Ctrl+C] to exit."))

	case StateSearching:
		doc.WriteString(fmt.Sprintf("Searching YouTube Music for: '%s'...\n\n", m.SearchQuery))
		doc.WriteString(dimStyle.Render("Resolving pristine topic stream stream..."))

	case StateDashboard:
		doc.WriteString("Active Ingestion Queue:\n\n")
		for _, track := range m.Tracks {
			statusSymbol := "⏳"
			if track.Status == "Done" {
				statusSymbol = "✓"
			}
			doc.WriteString(fmt.Sprintf("%s %-30s [%s]\n", statusSymbol, track.Title, track.Status))
		}
		doc.WriteString("\n");doc.WriteString(dimStyle.Render("Press [esc] to return to input | [q] to quit"))
	}

	if m.Err != nil {
		doc.WriteString(fmt.Sprintf("\n\nError: %v", m.Err))
	}

	return borderStyle.Render(doc.String())
}