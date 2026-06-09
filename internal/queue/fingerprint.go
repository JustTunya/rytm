package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

const (
	acoustIDEndpoint = "https://api.acoustid.org/v2/lookup"
	mbEndpoint       = "https://musicbrainz.org/ws/2"
	coverArtEndpoint = "https://coverartarchive.org/release"
	userAgent        = "rytm/1.0 (https://github.com/JustTunya/rytm)"
)

var fpHTTPClient = &http.Client{Timeout: 30 * time.Second}

// acoustIDLimiter ensures we do not exceed 2 requests per second to AcoustID
var acoustIDLimiter = rate.NewLimiter(rate.Every(time.Second/2), 1)

func acoustIDKey() string {
	if k := os.Getenv("ACOUSTID_KEY"); k != "" {
		return k
	}
	return "1vOwZtEn"
}

type TrackMeta struct {
	Title     string
	Artist    string
	Album     string
	Date      string
	TrackNum  string
	Genre     string
	CoverData []byte
	releaseID string
}

type fpcalcOutput struct {
	Duration    float64 `json:"duration"`
	Fingerprint string  `json:"fingerprint"`
}

func runFpcalc(ctx context.Context, audioPath string) (fingerprint string, duration int, err error) {
	cmd := exec.CommandContext(ctx, "fpcalc", "-json", audioPath)
	out, err := cmd.Output()
	if err != nil {
		return "", 0, fmt.Errorf("fpcalc exec: %w", err)
	}

	var result fpcalcOutput
	if err := json.Unmarshal(out, &result); err != nil {
		return "", 0, fmt.Errorf("fpcalc parse: %w", err)
	}
	if result.Fingerprint == "" {
		return "", 0, fmt.Errorf("fpcalc: empty fingerprint (file may be too short or silent)")
	}
	return result.Fingerprint, int(result.Duration), nil
}

func doWithRetry(ctx context.Context, buildReq func() (*http.Request, error)) (*http.Response, error) {
	var lastErr error
	backoff := time.Second
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
		req, err := buildReq()
		if err != nil {
			return nil, err
		}
		resp, err := fpHTTPClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}
		resp.Body.Close()
		lastErr = fmt.Errorf("rate limited (HTTP 429)")
	}
	return nil, fmt.Errorf("failed after 5 attempts: %w", lastErr)
}

type acoustIDResponse struct {
	Status  string           `json:"status"`
	Results []acoustIDResult `json:"results"`
}

type acoustIDResult struct {
	Score      float64             `json:"score"`
	Recordings []acoustIDRecording `json:"recordings"`
}

type acoustIDRecording struct {
	ID string `json:"id"`
}

func queryAcoustID(ctx context.Context, fingerprint string, duration int) (string, error) {
	// Enforce global rate limit of 2 requests per second
	if err := acoustIDLimiter.Wait(ctx); err != nil {
		return "", fmt.Errorf("acoustid rate limit wait: %w", err)
	}

	resp, err := doWithRetry(ctx, func() (*http.Request, error) {
		form := url.Values{}
		form.Set("client", acoustIDKey())
		form.Set("meta", "recordings")
		form.Set("duration", strconv.Itoa(duration))
		form.Set("fingerprint", fingerprint)

		req, err := http.NewRequestWithContext(
			ctx, http.MethodPost, acoustIDEndpoint,
			strings.NewReader(form.Encode()),
		)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", userAgent)
		return req, nil
	})
	if err != nil {
		return "", fmt.Errorf("acoustid: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("acoustid: HTTP %d - body: %s", resp.StatusCode, string(bodyBytes))
	}

	var result acoustIDResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("acoustid decode: %w", err)
	}
	if result.Status != "ok" {
		return "", fmt.Errorf("acoustid: status=%q", result.Status)
	}
	for _, r := range result.Results {
		if len(r.Recordings) > 0 {
			return r.Recordings[0].ID, nil
		}
	}
	return "", fmt.Errorf("acoustid: no recording IDs in response")
}

type mbRecording struct {
	Title        string           `json:"title"`
	ArtistCredit []mbArtistCredit `json:"artist-credit"`
	Releases     []mbRelease      `json:"releases"`
	Genres       []mbGenre        `json:"genres"`
}

type mbArtistCredit struct {
	Name       string   `json:"name"`
	Artist     mbArtist `json:"artist"`
	JoinPhrase string   `json:"joinphrase"`
}

type mbArtist struct {
	Name string `json:"name"`
}

type mbRelease struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Date         string         `json:"date"`
	ReleaseGroup mbReleaseGroup `json:"release-group"`
	Media        []mbMedia      `json:"media"`
	Genres       []mbGenre      `json:"genres"`
}

type mbReleaseGroup struct {
	PrimaryType string    `json:"primary-type"`
	Genres      []mbGenre `json:"genres"`
}

type mbMedia struct {
	Tracks []mbTrack `json:"tracks"`
}

type mbTrack struct {
	Number string `json:"number"`
}

type mbGenre struct {
	Name string `json:"name"`
}

func queryMusicBrainz(ctx context.Context, mbid string) (TrackMeta, error) {
	endpoint := fmt.Sprintf(
		"%s/recording/%s?inc=artists+releases+release-groups+media+genres&fmt=json",
		mbEndpoint, mbid,
	)

	resp, err := doWithRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "application/json")
		return req, nil
	})
	if err != nil {
		return TrackMeta{}, fmt.Errorf("musicbrainz: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return TrackMeta{}, fmt.Errorf("musicbrainz: HTTP %d", resp.StatusCode)
	}

	var rec mbRecording
	if err := json.NewDecoder(resp.Body).Decode(&rec); err != nil {
		return TrackMeta{}, fmt.Errorf("musicbrainz decode: %w", err)
	}

	meta := TrackMeta{Title: rec.Title}

	var sb strings.Builder
	for _, ac := range rec.ArtistCredit {
		name := ac.Name
		if name == "" {
			name = ac.Artist.Name
		}
		sb.WriteString(name)
		sb.WriteString(ac.JoinPhrase)
	}
	meta.Artist = strings.TrimSpace(sb.String())

	typePriority := map[string]int{"Album": 3, "EP": 2, "Single": 1}
	bestScore := -1
	var bestRelease *mbRelease
	for i := range rec.Releases {
		r := &rec.Releases[i]
		if s := typePriority[r.ReleaseGroup.PrimaryType]; s > bestScore {
			bestScore = s
			bestRelease = r
		}
	}
	if bestRelease != nil {
		meta.releaseID = bestRelease.ID
		meta.Album = bestRelease.Title
		if len(bestRelease.Date) >= 4 {
			meta.Date = bestRelease.Date[:4]
		}
		if len(bestRelease.Media) > 0 && len(bestRelease.Media[0].Tracks) > 0 {
			meta.TrackNum = bestRelease.Media[0].Tracks[0].Number
		}
	}

	var genre string
	if len(rec.Genres) > 0 {
		genre = rec.Genres[0].Name
	}
	if genre == "" && bestRelease != nil {
		if len(bestRelease.ReleaseGroup.Genres) > 0 {
			genre = bestRelease.ReleaseGroup.Genres[0].Name
		} else if len(bestRelease.Genres) > 0 {
			genre = bestRelease.Genres[0].Name
		}
	}
	if genre != "" {
		meta.Genre = strings.ToUpper(genre[:1]) + genre[1:]
	}

	return meta, nil
}

type coverArtIndex struct {
	Images []coverArtImage `json:"images"`
}

type coverArtImage struct {
	Front      bool               `json:"front"`
	Image      string             `json:"image"`
	Thumbnails coverArtThumbnails `json:"thumbnails"`
}

type coverArtThumbnails struct {
	Large  string `json:"large"`
	Px1200 string `json:"1200"`
}

func fetchCoverArt(ctx context.Context, releaseID string) ([]byte, error) {
	indexURL := fmt.Sprintf("%s/%s", coverArtEndpoint, releaseID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, indexURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := fpHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cover art index: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cover art index: HTTP %d", resp.StatusCode)
	}

	var index coverArtIndex
	if err := json.NewDecoder(resp.Body).Decode(&index); err != nil {
		return nil, fmt.Errorf("cover art decode: %w", err)
	}

	var chosen *coverArtImage
	for i := range index.Images {
		if index.Images[i].Front {
			chosen = &index.Images[i]
			break
		}
	}
	if chosen == nil && len(index.Images) > 0 {
		chosen = &index.Images[0]
	}
	if chosen == nil {
		return nil, fmt.Errorf("cover art: no images in response")
	}

	imageURL := bestCoverURL(*chosen)
	if imageURL == "" {
		return nil, fmt.Errorf("cover art: all URL fields empty")
	}

	imgReq, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, err
	}
	imgResp, err := fpHTTPClient.Do(imgReq)
	if err != nil {
		return nil, fmt.Errorf("cover art download: %w", err)
	}
	defer imgResp.Body.Close()

	if imgResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cover art download: HTTP %d", imgResp.StatusCode)
	}
	return io.ReadAll(imgResp.Body)
}

func bestCoverURL(img coverArtImage) string {
	if img.Thumbnails.Px1200 != "" {
		return img.Thumbnails.Px1200
	}
	if img.Thumbnails.Large != "" {
		return img.Thumbnails.Large
	}
	return img.Image
}

func FetchMetadata(ctx context.Context, audioPath string) (TrackMeta, error) {
	fingerprint, duration, err := runFpcalc(ctx, audioPath)
	if err != nil {
		return TrackMeta{}, err
	}

	mbid, err := queryAcoustID(ctx, fingerprint, duration)
	if err != nil {
		return TrackMeta{}, err
	}

	meta, err := queryMusicBrainz(ctx, mbid)
	if err != nil {
		return TrackMeta{}, err
	}

	if meta.releaseID != "" {
		if data, caErr := fetchCoverArt(ctx, meta.releaseID); caErr == nil {
			meta.CoverData = data
		}
	}

	return meta, nil
}
