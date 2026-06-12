package resolve

import "context"

// SearchResult is the raw output from a Provider.
type SearchResult struct {
	Entities []Entity
	RawQuery string
}

// Provider queries an external metadata source and returns parsed entities.
// Implementations must be safe for concurrent use.
type Provider interface {
	Search(ctx context.Context, query string) (SearchResult, error)
}

// ScoringStrategy scores and ranks a slice of entities against the original query.
// Returns entities sorted by descending score. Implementations must be stateless and pure.
type ScoringStrategy interface {
	Rank(query string, entities []Entity) []ScoredEntity
}

// ScoredEntity pairs an Entity with its computed relevance score.
type ScoredEntity struct {
	Entity Entity
	Score  float64
	Debug  string // Human-readable breakdown of scoring factors (for logging/testing)
}
