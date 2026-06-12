package resolve

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

var (
	reNoise = regexp.MustCompile(`(?i)\s*[\(\[][^\]\)]*(official|video|audio|lyric|clip|hq|hd|remastered|mono|stereo)[^\]\)]*[\)\]]`)
	reFeat  = regexp.MustCompile(`(?i)\s*feat\..*`)
	rePunct = regexp.MustCompile(`[^\w\s]`)
)

type DefaultScoring struct{}

func (s DefaultScoring) Rank(query string, entities []Entity) []ScoredEntity {
	normalizedQuery := cleanText(query)
	queryWords := strings.Fields(normalizedQuery)

	var scored []ScoredEntity

	for _, entity := range entities {
		score, debugLog := s.scoreEntity(normalizedQuery, queryWords, entity)
		scored = append(scored, ScoredEntity{
			Entity: entity,
			Score:  score,
			Debug:  debugLog,
		})
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	return scored
}

func (s DefaultScoring) scoreEntity(query string, queryWords []string, entity Entity) (float64, string) {
	var score float64
	var debug strings.Builder

	// 1. Base Weight
	switch entity.Type {
	case EntitySong:
		score += 100
		debug.WriteString("Base(Song)=100 ")
	case EntityAlbum:
		score += 80
		debug.WriteString("Base(Album)=80 ")
	case EntityVideo:
		score += 60
		debug.WriteString("Base(Video)=60 ")
	case EntityPlaylist:
		score += 40
		debug.WriteString("Base(Playlist)=40 ")
	}

	normTitle := cleanText(entity.Title)

	// 2. Exact Title Match
	if query == normTitle {
		score += 50
		debug.WriteString("ExactTitle=50 ")
	} else if len(queryWords) > 0 {
		// 3. Partial Title Match (all query words in title)
		allFound := true
		for _, w := range queryWords {
			if !strings.Contains(normTitle, w) {
				allFound = false
				break
			}
		}
		if allFound {
			score += 20
			debug.WriteString("PartialTitle=20 ")
		}
	}

	// 4. Exact Artist Match
	for _, a := range entity.Artists {
		normArtist := cleanText(a)
		if strings.Contains(query, normArtist) {
			score += 30
			debug.WriteString(fmt.Sprintf("ExactArtist(%s)=30 ", a))
			break
		}
	}

	// 5. Official Music Video Flag
	if entity.IsOfficialMV {
		score += 15
		debug.WriteString("OfficialMV=15 ")
	}

	// 6. Explicit Flag
	if entity.IsExplicit {
		score += 5
		debug.WriteString("Explicit=5 ")
	}

	// 7. Duration Alignment (for songs/videos)
	// Typical song is 180s - 300s (3 to 5 mins). Bonus if within this range,
	// and penalty if extremely long/short unless it's a playlist/album.
	if (entity.Type == EntitySong || entity.Type == EntityVideo) && entity.DurationSec > 0 {
		if entity.DurationSec >= 120 && entity.DurationSec <= 420 {
			score += 10
			debug.WriteString("DurationAlign=10 ")
		} else if entity.DurationSec > 600 {
			score -= 20
			debug.WriteString("DurationPenalty(>10m)=-20 ")
		}
	}

	// 8. Track Count Check for Albums (Filter out singles disguised as albums)
	if entity.Type == EntityAlbum {
		if entity.TrackCount == 1 {
			score -= 30
			debug.WriteString("SingleTrackAlbumPenalty=-30 ")
		}
	}

	return math.Max(0, score), strings.TrimSpace(debug.String())
}

func cleanText(s string) string {
	s = reNoise.ReplaceAllString(s, "")
	s = reFeat.ReplaceAllString(s, "")
	s = rePunct.ReplaceAllString(s, " ") // replace punctuation with spaces
	s = strings.ToLower(s)
	// collapse multiple spaces
	return strings.Join(strings.Fields(s), " ")
}
