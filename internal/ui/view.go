package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	// Minimalist styling components matching a crisp terminal look
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#2980b9")).Bold(true)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#2c3e50"))
	greyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#7f8c8d"))
	borderStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#34495e")).Padding(1, 2)

	// Table columns styles for playlists
	numStyle    = lipgloss.NewStyle().Width(5)
	titleStyle  = lipgloss.NewStyle().Width(35)
	artistStyle = lipgloss.NewStyle().Width(20)
	albumStyle  = lipgloss.NewStyle().Width(20)
	statusStyle = lipgloss.NewStyle().Width(20)

	// Loading Spinner Frames
	spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
)

func (m Model) View() string {
	contentWidth := m.WindowWidth - 6
	if contentWidth < 60 {
		contentWidth = 60
	}

	availableColumnsWidth := contentWidth - 25
	if availableColumnsWidth < 30 {
		availableColumnsWidth = 30
	}
	baseWidth := availableColumnsWidth / 3

	titleWidth := baseWidth + (availableColumnsWidth % 3)
	artistWidth := baseWidth
	albumWidth := baseWidth

	dynamicTitleStyle := titleStyle.Width(titleWidth)
	dynamicArtistStyle := artistStyle.Width(artistWidth)
	dynamicAlbumStyle := albumStyle.Width(albumWidth)

	var doc strings.Builder

	doc.WriteString(accentStyle.Render("RYTM // Media Ingestion Engine"))
	doc.WriteString("\n\n")

	switch m.State {
	case StateInput:
		doc.WriteString("Search Index / Ingest Stream:\n")
		doc.WriteString(m.TextInput.View())
		doc.WriteString("\n\n")
		doc.WriteString(dimStyle.Render("Press [q] or [Ctrl+C] to exit."))

	case StateSearching:
		doc.WriteString(fmt.Sprintf("Searching YouTube Music for: '%s'...\n\n", m.SearchQuery))
		doc.WriteString(dimStyle.Render("Resolving pristine topic stream stream..."))

	case StateDashboard:
		doc.WriteString("Active Ingestion Queue:\n\n")

		var singles []TrackItem
		playlists := make(map[string][]TrackItem)
		var playlistOrder []string

		for _, t := range m.Tracks {
			if t.IsPlaylist && t.PlaylistTrackNum > 0 {
				if _, ok := playlists[t.PlaylistName]; !ok {
					playlistOrder = append(playlistOrder, t.PlaylistName)
				}
				playlists[t.PlaylistName] = append(playlists[t.PlaylistName], t)
			} else {
				if t.IsPlaylist && t.PlaylistTrackNum == 0 && t.Status == "Done" {
					continue
				}
				singles = append(singles, t)
			}
		}

		var flatItems []TrackItem
		flatItems = append(flatItems, singles...)

		for _, pName := range playlistOrder {
			pTracks := playlists[pName]
			sort.Slice(pTracks, func(i, j int) bool {
				return pTracks[i].PlaylistTrackNum < pTracks[j].PlaylistTrackNum
			})
			flatItems = append(flatItems, pTracks...)
		}

		if len(flatItems) == 0 {
			doc.WriteString(dimStyle.Render("Queue is empty."))
		} else {
			maxOffset := len(flatItems) - 6
			if maxOffset < 0 {
				maxOffset = 0
			}

			offset := m.ScrollOffset
			if offset > maxOffset {
				offset = maxOffset
			}
			if offset < 0 {
				offset = 0
			}

			start := offset
			end := start + 6
			if end > len(flatItems) {
				end = len(flatItems)
			}
			displayItems := flatItems[start:end]

			lastPlaylist := ""

			for i, track := range displayItems {
				statusSymbol := " "
				switch track.Status {
				case "Done":
					statusSymbol = "✔"
				case "Failed", "Cancelled":
					statusSymbol = "✖"
				default:
					statusSymbol = spinnerFrames[m.FrameIndex%len(spinnerFrames)]
				}

				if track.IsPlaylist && track.PlaylistTrackNum > 0 {
					if track.PlaylistName != lastPlaylist {
						if i > 0 {
							doc.WriteString("\n")
						}
						renderPlaylistHeader(track.PlaylistName, &doc, dynamicTitleStyle, dynamicArtistStyle, dynamicAlbumStyle)
						lastPlaylist = track.PlaylistName
					}

					statusText := track.Status

					title := track.Title
					if title == "" {
						title = fmt.Sprintf("Track %d", track.PlaylistTrackNum)
					}
					if titleWidth > 5 && len(title) > titleWidth-2 {
						title = title[:titleWidth-5] + "..."
					}

					artist := track.Artist
					if artistWidth > 5 && len(artist) > artistWidth-2 {
						artist = artist[:artistWidth-5] + "..."
					}

					album := track.Album
					if albumWidth > 5 && len(album) > albumWidth-2 {
						album = album[:albumWidth-5] + "..."
					}

					numStr := fmt.Sprintf("%d", track.PlaylistTrackNum)

					row := numStyle.Render(numStr) +
						dynamicTitleStyle.Render(title) +
						dynamicArtistStyle.Render(artist) +
						dynamicAlbumStyle.Render(album) +
						statusStyle.Render(statusSymbol+" "+statusText)

					doc.WriteString(row)
					doc.WriteString("\n")
				} else {
					lastPlaylist = ""

					isPendingOrRunning := track.Status == "Pending" || track.Status == "Resolving" || track.Status == "Downloading" || track.Status == "Fingerprinting" || track.Status == "Tagging" || track.Status == "Queued" || track.Status == "Fetching Playlist"

					statusText := track.Status
					if track.Status == "Failed" && track.Error != "" {
						statusText = fmt.Sprintf("Failed: %s", track.Error)
					}

					if isPendingOrRunning && (track.Title == "" || track.Artist == "") {
						doc.WriteString(fmt.Sprintf("%s Searching for %s... [%s]\n", statusSymbol, track.Query, statusText))
					} else {
						title := track.Title
						if title == "" {
							title = track.Query
						}

						maxTitleLen := contentWidth - 30
						if maxTitleLen < 30 {
							maxTitleLen = 30
						}
						if len(title) > maxTitleLen {
							title = title[:maxTitleLen-3] + "..."
						}

						doc.WriteString(fmt.Sprintf("%s %-*s [%s]\n", statusSymbol, maxTitleLen, title, statusText))

						artist := track.Artist
						if artist == "" {
							artist = "Unknown Artist"
						}
						doc.WriteString(fmt.Sprintf("  %s\n", greyStyle.Render(artist)))
					}
				}
			}

			if len(flatItems) > 6 {
				doc.WriteString("\n")
				indicator := fmt.Sprintf("↑/↓ to scroll | [%d-%d of %d items]", start+1, end, len(flatItems))
				rightAligned := lipgloss.NewStyle().Align(lipgloss.Right).Width(contentWidth).Render(indicator)
				doc.WriteString(dimStyle.Render(rightAligned))
				doc.WriteString("\n")
			}
		}

		doc.WriteString("\n")
		doc.WriteString(dimStyle.Render("[esc] to return | [q] to quit"))
	}

	if m.Err != nil {
		doc.WriteString(fmt.Sprintf("\n\nError: %v", m.Err))
	}

	return borderStyle.Width(contentWidth + 4).Render(doc.String())
}

func renderPlaylistHeader(name string, doc *strings.Builder, dynamicTitleStyle lipgloss.Style, dynamicArtistStyle lipgloss.Style, dynamicAlbumStyle lipgloss.Style) {
	doc.WriteString(accentStyle.Render("Playlist: " + name))
	doc.WriteString("\n")
	header := numStyle.Render("#") + dynamicTitleStyle.Render("Title") + dynamicArtistStyle.Render("Artist") + dynamicAlbumStyle.Render("Album") + statusStyle.Render("Status")
	doc.WriteString(dimStyle.Render(header))
	doc.WriteString("\n")
}
