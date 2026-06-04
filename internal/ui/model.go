package ui

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// SessionState tracks the active viewport of the TUI
type SessionState int

const (
	StateInput SessionState = iota
	StateSearching
	StateDashboard
)

type TrackItem struct {
	Title    string
	Status   string // "Downloading", "Tagging", "Done"
	Progress float64
}

type Model struct {
	State       SessionState
	TextInput   textinput.Model
	Tracks      []TrackItem
	SearchQuery string
	Err         error
}

func InitialModel() Model {
	ti := textinput.New()
	ti.Placeholder = "Paste YouTube URL or type song name..."
	ti.Focus()
	ti.CharLimit = 156
	ti.Width = 50

	return Model{
		State:     StateInput,
		TextInput: ti,
		Tracks:    []TrackItem{},
	}
}

func (m Model) Init() tea.Cmd {
	return textinput.Blink
}