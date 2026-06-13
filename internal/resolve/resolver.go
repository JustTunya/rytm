package resolve

import (
	"context"
	"fmt"
)

// Resolver is the entry point for pre-fetch query resolution.
type Resolver struct {
	provider Provider
	scorer   ScoringStrategy
}

// NewResolver creates a new Resolver orchestrator.
func NewResolver(p Provider, s ScoringStrategy) *Resolver {
	return &Resolver{
		provider: p,
		scorer:   s,
	}
}

// Confidence represents the engine's decision on a resolution result.
type Confidence int

const (
	ConfidenceHigh Confidence = iota // Auto-queue: score delta exceeds threshold
	ConfidenceLow                    // Disambiguate: present choices to user
)

// FullResult is the output of ResolveAll, containing all scored candidates.
type FullResult struct {
	Confidence Confidence
	Winner     ScoredEntity   // Top-scoring entity
	Candidates []ScoredEntity // All scored entities, descending by score (capped at MaxCandidates)
}

const (
	ConfidenceThreshold = 40.0 // Min score delta between #1 and #2 for auto-queue
	MaxCandidates       = 20   // Max entities returned for disambiguation
)

// Result is the output of a successful resolution.
type Result struct {
	URL        string     // Canonical YouTube URL to pass to yt-dlp
	Entity     Entity     // The winning entity
	Score      float64    // Winning score
	EntityType EntityType // Convenience accessor
	IsMulti    bool       // True if the result is an album/playlist (multi-track)
	Tracks     []Entity   // Populated for albums/playlists (if supported by provider in the future)
}

// Resolve executes the full resolution pipeline for a raw text query.
// Returns the highest-scoring result, or a sentinel error.
func (r *Resolver) Resolve(ctx context.Context, query string) (Result, error) {
	// 1. Fetch raw search results
	searchRes, err := r.provider.Search(ctx, query)
	if err != nil {
		return Result{}, err // Returns ErrNoResults, ErrRateLimited, etc.
	}

	if len(searchRes.Entities) == 0 {
		return Result{}, ErrNoResults
	}

	// 2. Score and rank entities
	scored := r.scorer.Rank(query, searchRes.Entities)
	if len(scored) == 0 {
		return Result{}, ErrNoResults
	}

	// 3. Select top-scored entity
	winner := scored[0]
	
	// If score is 0, none of the results are relevant enough
	if winner.Score <= 0 {
		return Result{}, ErrNoResults
	}

	url := winner.Entity.URL()
	if url == "" {
		return Result{}, fmt.Errorf("%w: top result has no valid URL", ErrBadResponse)
	}

	isMulti := winner.Entity.Type == EntityAlbum || winner.Entity.Type == EntityPlaylist

	return Result{
		URL:        url,
		Entity:     winner.Entity,
		Score:      winner.Score,
		EntityType: winner.Entity.Type,
		IsMulti:    isMulti,
	}, nil
}

// ResolveAll executes resolution and returns the full ranked list with confidence.
func (r *Resolver) ResolveAll(ctx context.Context, query string) (FullResult, error) {
	searchRes, err := r.provider.Search(ctx, query)
	if err != nil {
		return FullResult{}, err
	}
	if len(searchRes.Entities) == 0 {
		return FullResult{}, ErrNoResults
	}

	scored := r.scorer.Rank(query, searchRes.Entities)
	if len(scored) == 0 || scored[0].Score <= 0 {
		return FullResult{}, ErrNoResults
	}

	// Cap candidates and filter by score delta (close enough score)
	var candidates []ScoredEntity
	topScore := scored[0].Score
	for _, sc := range scored {
		if len(candidates) >= MaxCandidates {
			break
		}
		if topScore-sc.Score < ConfidenceThreshold {
			candidates = append(candidates, sc)
		}
	}

	// Determine confidence via score delta
	confidence := ConfidenceHigh
	if len(scored) >= 2 {
		delta := scored[0].Score - scored[1].Score
		if delta < ConfidenceThreshold {
			confidence = ConfidenceLow
		}
	}

	return FullResult{
		Confidence: confidence,
		Winner:     scored[0],
		Candidates: candidates,
	}, nil
}
