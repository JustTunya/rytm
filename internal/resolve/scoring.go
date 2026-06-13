package resolve

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

var (
	reNoise  = regexp.MustCompile(`(?i)\s*[\(\[][^\]\)]*(official|video|audio|lyric|clip|hq|hd|remastered|mono|stereo|ep|single|explicit|deluxe|album)[^\]\)]*[\)\]]`)
	reFeat   = regexp.MustCompile(`(?i)\s*feat\..*`)
	reSuffix = regexp.MustCompile(`(?i)\s*-\s*(ep|single|album|deluxe)$`)
	rePunct  = regexp.MustCompile(`[^\w\s]`)
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
		score += 95
		debug.WriteString("Base(Album)=95 ")
	case EntityVideo:
		score += 60
		debug.WriteString("Base(Video)=60 ")
	case EntityPlaylist:
		score += 40
		debug.WriteString("Base(Playlist)=40 ")
	}

	normTitle := cleanText(entity.Title)
	normTitleAndArtists := cleanText(strings.Join(entity.Artists, " ") + " " + entity.Title)

	// 2. Exact Title Match
	if query == normTitle {
		score += 50
		debug.WriteString("ExactTitle=50 ")
	} else if normTitle != "" && strings.Contains(query, normTitle) {
		// If the query contains the exact title (e.g. query is "Artist - Title", title is "Title")
		// This avoids penalizing the official track which doesn't have the artist in the title field.
		score += 40
		debug.WriteString("TitleInQuery=40 ")
	} else if len(queryWords) > 0 {
		// 3. Partial Title Match (all query words in title+artist)
		allFound := true
		for _, w := range queryWords {
			if !strings.Contains(normTitleAndArtists, w) {
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
		if normArtist != "" && strings.Contains(query, normArtist) {
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


	return math.Max(0, score), strings.TrimSpace(debug.String())
}

func cleanText(s string) string {
	s = reNoise.ReplaceAllString(s, "")
	s = reFeat.ReplaceAllString(s, "")
	s = reSuffix.ReplaceAllString(s, "")
	s = rePunct.ReplaceAllString(s, " ") // replace punctuation with spaces
	s = strings.ToLower(s)
	// collapse multiple spaces
	return strings.Join(strings.Fields(s), " ")
}
