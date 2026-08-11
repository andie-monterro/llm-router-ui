package keys

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/michaelquigley/df/dl"
)

// Snapshot is an immutable composed view of all installed contributions.
type Snapshot struct {
	SchemaVersion int
	Generation    uint64
	byDigest      map[[sha256.Size]byte]*Record
}

type sourceState struct {
	contribution *Contribution
	loadedAt     time.Time
	excluded     bool
	reloadable   bool
}

type closeableSource interface {
	close() error
}

type reloadableSource struct {
	source   Source
	interval time.Duration
	required bool
}

// HTTPClientFactory supplies the borrowed client for an HTTP key source.
// transport construction remains owned by the gateway.
type HTTPClientFactory func(*HTTPSourceConfig) (*http.Client, error)

// Store owns source contributions, composition, and the resident snapshot. it
// is the only key lookup surface used by the data plane.
type Store struct {
	mu     sync.Mutex
	order  []string
	states map[string]*sourceState

	snapshot     atomic.Pointer[Snapshot]
	clock        func() time.Time
	newTimer     deadlineTimerFactory
	maxStaleness time.Duration
	meters       *meters
	booting      bool

	deadlineSignal  chan struct{}
	deadlineAt      time.Time
	deadlineArmed   bool
	deadlineVersion uint64

	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	runners   []*runner
	closeable []closeableSource
	closeOnce sync.Once
}

// NewStore maps the boot-resident config contribution and publishes it as the
// initial snapshot. it remains the compact constructor for config-only users.
func NewStore(entries []EntryConfig) (*Store, error) {
	return newStore(entries, time.Now)
}

func newStore(entries []EntryConfig, clock func() time.Time) (*Store, error) {
	store := newEmptyStore(clock)
	if err := store.initMeters(); err != nil {
		store.Close()
		return nil, fmt.Errorf("initialize API key metrics: %w", err)
	}
	configSource := newConfigSource(entries)
	if err := store.registerSource(configSource, 0); err != nil {
		store.Close()
		return nil, err
	}
	if err := store.bootLoad(configSource, true); err != nil {
		store.Close()
		return nil, err
	}
	store.finishBoot()
	return store, nil
}

// NewStoreFromConfig assembles config, file, and HTTP contributions, performs
// every initial load, and only then starts reload loops.
func NewStoreFromConfig(cfg *Config, clientForHTTP HTTPClientFactory) (_ *Store, err error) {
	if cfg == nil {
		return nil, fmt.Errorf("api key config is required")
	}
	if err := Validate(cfg); err != nil {
		return nil, err
	}
	store := newEmptyStore(time.Now)
	if cfg.Reload != nil {
		store.maxStaleness = cfg.Reload.MaxStaleness
	}
	if err := store.initMeters(); err != nil {
		store.Close()
		return nil, fmt.Errorf("initialize API key metrics: %w", err)
	}
	defer func() {
		if err != nil {
			store.Close()
		}
	}()

	configSource := newConfigSource(cfg.Keys)
	if err := store.registerSource(configSource, 0); err != nil {
		return nil, err
	}
	if err := store.bootLoad(configSource, true); err != nil {
		return nil, err
	}

	reloadables := make([]reloadableSource, 0, len(cfg.Sources))
	for _, dynamic := range cfg.Sources {
		var source Source
		var interval time.Duration
		var required bool
		switch sourceCfg := dynamic.(type) {
		case *FileSourceConfig:
			fileSource, sourceErr := newFileSource(sourceCfg)
			if sourceErr != nil {
				return nil, sourceErr
			}
			store.closeable = append(store.closeable, fileSource)
			source = fileSource
			interval = sourceCfg.PollInterval
			required = sourceCfg.required()
		case *HTTPSourceConfig:
			if clientForHTTP == nil {
				return nil, fmt.Errorf("key source '%s' requires an injected HTTP client", sourceCfg.Name)
			}
			client, clientErr := clientForHTTP(sourceCfg)
			if clientErr != nil {
				return nil, fmt.Errorf("key source '%s': create HTTP client: %w", sourceCfg.Name, clientErr)
			}
			httpSource, sourceErr := newHTTPSource(sourceCfg, client)
			if sourceErr != nil {
				return nil, sourceErr
			}
			source = httpSource
			interval = sourceCfg.PollInterval
			required = sourceCfg.required()
		default:
			return nil, fmt.Errorf("unsupported key source config %T", dynamic)
		}
		if err := store.registerSource(source, interval); err != nil {
			return nil, err
		}
		reloadables = append(reloadables, reloadableSource{
			source:   source,
			interval: interval,
			required: required,
		})
	}

	for _, item := range reloadables {
		if err := store.bootLoad(item.source, item.required); err != nil {
			return nil, err
		}
	}
	store.finishBoot()
	dl.Infof("loaded API key sources in precedence order %v with record counts %v", store.order, store.bootRecordCounts())
	store.start(reloadables)
	return store, nil
}

func (s *Store) bootRecordCounts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	counts := make([]string, 0, len(s.order))
	for _, name := range s.order {
		state := s.states[name]
		if state.contribution == nil {
			counts = append(counts, name+"=unavailable")
			continue
		}
		counts = append(counts, fmt.Sprintf("%s=%d", name, len(state.contribution.Records)))
	}
	return counts
}

func newEmptyStore(clock func() time.Time) *Store {
	return newEmptyStoreWithTimer(clock, func(after time.Duration) deadlineTimer {
		return newSystemDeadlineTimer(after)
	})
}

func newEmptyStoreWithTimer(clock func() time.Time, newTimer deadlineTimerFactory) *Store {
	ctx, cancel := context.WithCancel(context.Background())
	return &Store{
		states:         make(map[string]*sourceState),
		clock:          clock,
		newTimer:       newTimer,
		booting:        true,
		deadlineSignal: make(chan struct{}, 1),
		ctx:            ctx,
		cancel:         cancel,
	}
}

func (s *Store) registerSource(source Source, interval time.Duration) error {
	name := source.Name()
	if _, exists := s.states[name]; exists {
		return fmt.Errorf("key source '%s' is already registered", name)
	}
	s.order = append(s.order, name)
	s.states[name] = &sourceState{reloadable: interval > 0}
	return nil
}

func (s *Store) bootLoad(source Source, required bool) error {
	result, err := source.Load(s.ctx)
	if err == nil {
		err = result.validate()
	}
	if err != nil {
		if required {
			return fmt.Errorf("load key source '%s' at boot: %w", source.Name(), err)
		}
		dl.Errorf("optional key source '%s' failed its boot load: %v", source.Name(), err)
		return nil
	}
	if result.IsUnchanged() {
		return fmt.Errorf("load key source '%s' at boot: source confirmed data it has never loaded", source.Name())
	}
	if err := s.Install(source.Name(), result.Contribution(), s.clock()); err != nil {
		return fmt.Errorf("load key source '%s' at boot: %w", source.Name(), err)
	}
	return nil
}

func (s *Store) finishBoot() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.booting = false
	s.recompose()
}

// Install is the only path by which a contribution enters the union. it takes
// a defensive deep copy before serializing the store-wide composition.
func (s *Store) Install(source string, contribution *Contribution, at time.Time) error {
	if contribution == nil {
		return fmt.Errorf("source '%s' returned a nil contribution", source)
	}
	if contribution.SchemaVersion != 1 {
		return fmt.Errorf("source '%s' returned unsupported schema version %d", source, contribution.SchemaVersion)
	}
	owned := contribution.clone(source)
	for i, record := range owned.Records {
		if record == nil {
			return fmt.Errorf("source '%s' returned nil record at index %d", source, i)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	state, exists := s.states[source]
	if !exists {
		return fmt.Errorf("key source '%s' is not registered", source)
	}
	wasExcluded := state.excluded
	state.contribution = owned
	state.loadedAt = at
	state.excluded = false
	s.recompose()
	if wasExcluded {
		dl.Infof("key source '%s' reinstated after a successful refresh", source)
	}
	return nil
}

// Touch records a successful confirmation without replacing resident data.
func (s *Store) Touch(source string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, exists := s.states[source]
	if !exists {
		return fmt.Errorf("key source '%s' is not registered", source)
	}
	if state.contribution == nil {
		return fmt.Errorf("key source '%s' has no contribution to confirm", source)
	}
	wasExcluded := state.excluded
	state.loadedAt = at
	state.excluded = false
	if wasExcluded {
		s.recompose()
		dl.Infof("key source '%s' reinstated after a successful refresh", source)
	} else {
		s.armDeadlineLocked()
	}
	return nil
}

func (s *Store) recompose() {
	winners := make(map[[sha256.Size]byte]string)
	winnerRecords := make(map[[sha256.Size]byte]*Record)
	for _, source := range s.order {
		state := s.states[source]
		if state.contribution == nil {
			continue
		}
		for _, record := range state.contribution.Records {
			if winner, duplicate := winners[record.Digest]; duplicate {
				if !s.booting {
					held := winnerRecords[record.Digest]
					dl.Warnf("API key digest collision: source '%s' entry '%s' wins over source '%s' entry '%s'", winner, held.Name, source, record.Name)
				}
				continue
			}
			winners[record.Digest] = source
			winnerRecords[record.Digest] = record
		}
	}

	next := make(map[[sha256.Size]byte]*Record)
	emitted := make([]*Record, 0, len(winners))
	for _, source := range s.order {
		state := s.states[source]
		if state.excluded || state.contribution == nil {
			continue
		}
		for _, record := range state.contribution.Records {
			if winners[record.Digest] == source {
				next[record.Digest] = record
				emitted = append(emitted, record)
			}
		}
	}

	if s.booting {
		return
	}
	byName := make(map[string]*Record)
	for _, record := range emitted {
		if held, duplicate := byName[record.Name]; duplicate {
			dl.Infof("API key name '%s' is shared by source '%s' and source '%s'", record.Name, held.Source, record.Source)
			continue
		}
		byName[record.Name] = record
	}
	if len(next) == 0 {
		dl.Warn("composed API key snapshot is empty; all authenticated requests will be denied")
	}

	generation := uint64(1)
	if current := s.snapshot.Load(); current != nil {
		generation = current.Generation + 1
	}
	s.snapshot.Store(&Snapshot{
		SchemaVersion: 1,
		Generation:    generation,
		byDigest:      next,
	})
	s.armDeadlineLocked()
}

func (s *Store) start(sources []reloadableSource) {
	if s.maxStaleness > 0 {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.runStalenessEvaluator()
		}()
	}
	for _, item := range sources {
		runner := newRunner(item.source, s, item.interval)
		s.runners = append(s.runners, runner)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			runner.run(s.ctx)
		}()
		if watcher, ok := item.source.(Watcher); ok {
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				if err := watcher.Watch(s.ctx, runner.kick); err != nil && s.ctx.Err() == nil {
					dl.Errorf("key source '%s' watcher stopped: %v", item.source.Name(), err)
				}
			}()
		}
	}
}

// TriggerAll requests one refresh from every reloadable source and returns the
// number of runners notified.
func (s *Store) TriggerAll() int {
	if s == nil {
		return 0
	}
	for _, runner := range s.runners {
		runner.kick()
	}
	return len(s.runners)
}

// Close stops and joins every source loop before closing watcher resources.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	var closeErr error
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		s.wg.Wait()
		for _, source := range s.closeable {
			if err := source.close(); err != nil && closeErr == nil {
				closeErr = err
			}
		}
		if s.meters != nil {
			if err := s.meters.close(); err != nil && closeErr == nil {
				closeErr = err
			}
		}
	})
	return closeErr
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
