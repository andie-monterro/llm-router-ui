package keys

import (
	"context"
	"crypto/sha256"
	"sync/atomic"
	"time"
)

// Snapshot is an immutable composed view of all installed contributions.
type Snapshot struct {
	SchemaVersion int
	Generation    uint64
	byDigest      map[[sha256.Size]byte]*Record
}

// Store owns the resident key snapshot and is the only key lookup surface used
// by the data plane.
type Store struct {
	snapshot atomic.Pointer[Snapshot]
	clock    func() time.Time
}

// NewStore maps the boot-resident config contribution and publishes it as the
// initial snapshot. reloadable source composition joins this path later.
func NewStore(entries []EntryConfig) (*Store, error) {
	return newStore(entries, time.Now)
}

func newStore(entries []EntryConfig, clock func() time.Time) (*Store, error) {
	contribution, err := newConfigSource(entries).load(context.Background())
	if err != nil {
		return nil, err
	}
	byDigest := make(map[[sha256.Size]byte]*Record, len(contribution.Records))
	for _, record := range contribution.Records {
		byDigest[record.Digest] = record
	}
	store := &Store{clock: clock}
	store.snapshot.Store(&Snapshot{
		SchemaVersion: contribution.SchemaVersion,
		Generation:    1,
		byDigest:      byDigest,
	})
	return store, nil
}

// Lookup hashes the exact bearer-token bytes, then evaluates expiry at lookup
// so an expiry boundary never drifts with refresh cadence.
func (s *Store) Lookup(token string) (*Record, bool) {
	if s == nil {
		return nil, false
	}
	snapshot := s.snapshot.Load()
	if snapshot == nil {
		return nil, false
	}
	record, ok := snapshot.byDigest[sha256.Sum256([]byte(token))]
	if !ok || record.Expired(s.clock()) {
		return nil, false
	}
	return record, true
}
