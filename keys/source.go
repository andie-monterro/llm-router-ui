package keys

import (
	"context"
	"fmt"
)

// Source produces its complete current contribution. ownership of an updated
// contribution transfers to the store when Load returns successfully.
type Source interface {
	Name() string
	Load(context.Context) (LoadResult, error)
}

// Watcher optionally supplies a low-latency change signal. polling remains the
// convergence floor, so a watcher only requests the ordinary refresh path.
type Watcher interface {
	Watch(context.Context, func()) error
}

// LoadResult distinguishes a new contribution from confirmation that the
// resident contribution is unchanged. exactly one arm must be set.
type LoadResult struct {
	unchanged    bool
	contribution *Contribution
}

// Updated reports a newly loaded complete contribution.
func Updated(contribution *Contribution) LoadResult {
	return LoadResult{contribution: contribution}
}

// Unchanged confirms the resident contribution without replacing it.
func Unchanged() LoadResult {
	return LoadResult{unchanged: true}
}

// IsUnchanged reports whether this result is the confirmation arm.
func (r LoadResult) IsUnchanged() bool {
	return r.unchanged
}

// Contribution returns the updated contribution arm, if present.
func (r LoadResult) Contribution() *Contribution {
	return r.contribution
}

func (r LoadResult) validate() error {
	if r.unchanged == (r.contribution != nil) {
		return fmt.Errorf("source returned invalid load result")
	}
	return nil
}
