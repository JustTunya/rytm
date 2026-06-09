package queue

import (
	"regexp"
	"strings"
)

// Precompile regexes for title cleaning to optimize performance and reduce allocation overhead.
var (
	reNoise = regexp.MustCompile(`(?i)\s*[\(\[][^\]\)]*(official|video|audio|lyric|clip|hq|hd|remastered|mono|stereo)[^\]\)]*[\)\]]`)
	reFeat  = regexp.MustCompile(`(?i)\s*feat\..*`)
)

func parseFallbackMeta(videoTitle string) TrackMeta {
	meta := TrackMeta{
		Title:  videoTitle,
		Artist: "Unknown Artist",
		Album:  "YouTube",
	}
	parts := strings.SplitN(videoTitle, " - ", 2)
	if len(parts) == 2 {
		meta.Artist = strings.TrimSpace(parts[0])
		meta.Title = strings.TrimSpace(parts[1])
	} else {
		for _, dash := range []string{" – ", " — ", " : "} {
			parts = strings.SplitN(videoTitle, dash, 2)
			if len(parts) == 2 {
				meta.Artist = strings.TrimSpace(parts[0])
				meta.Title = strings.TrimSpace(parts[1])
				break
			}
		}
	}
	meta.Title = cleanTitle(meta.Title)
	meta.Artist = cleanTitle(meta.Artist)
	return meta
}
func cleanTitle(s string) string {
	s = reNoise.ReplaceAllString(s, "")
	s = reFeat.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}
func buildOutputName(meta TrackMeta, videoTitle string, query string) string {
	title := sanitizeFilename(meta.Title)
	if title != "" {
		return title + ".m4a"
	}
	vTitle := sanitizeFilename(videoTitle)
	if vTitle != "" {
		return vTitle + ".m4a"
	}
	return sanitizeFilename(query) + ".m4a"
}
func sanitizeFilename(s string) string {
	r := strings.NewReplacer(
		"/", "-", "\\", "-", ":", " -",
		"*", "", "?", "", "\"", "'",
		"<", "", ">", "", "|", "-",
	)
	return strings.TrimSpace(r.Replace(s))
}
func TestParseFallbackMeta(title string) TrackMeta {
	return parseFallbackMeta(title)
}
