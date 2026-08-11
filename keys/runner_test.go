package keys

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type blockingSource struct {
	name       string
	started    chan int
	release    chan struct{}
	calls      atomic.Int32
	active     atomic.Int32
	maxActive  atomic.Int32
	result     LoadResult
	respectCtx bool
}

func (s *blockingSource) Name() string {
	return s.name
}

func (s *blockingSource) Load(ctx context.Context) (LoadResult, error) {
	call := int(s.calls.Add(1))
	active := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		maximum := s.maxActive.Load()
		if active <= maximum || s.maxActive.CompareAndSwap(maximum, active) {
			break
		}
	}
	s.started <- call
	if s.respectCtx {
		select {
		case <-ctx.Done():
			return LoadResult{}, ctx.Err()
		case <-s.release:
		}
	} else {
		<-s.release
	}
	return s.result, nil
}

func waitCall(t *testing.T, started <-chan int, want int) {
	t.Helper()
	select {
	case got := <-started:
		if got != want {
			t.Fatalf("load call = %d, want %d", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("load call %d did not start", want)
	}
}

func TestRunnerCollapsesBurstAndSerializesLoads(t *testing.T) {
	store := newCompositionStore(t, "managed")
	source := &blockingSource{
		name:    "managed",
		started: make(chan int, 8),
		release: make(chan struct{}, 8),
		result:  Updated(contributionFor(t, "alice", "sk-alice")),
	}
	runner := newRunner(source, store, time.Hour)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		runner.run(ctx)
		close(done)
	}()

	runner.kick()
	waitCall(t, source.started, 1)
	for i := 0; i < 100; i++ {
		runner.kick()
	}
	source.release <- struct{}{}
	waitCall(t, source.started, 2)
	source.release <- struct{}{}
	time.Sleep(25 * time.Millisecond)
	if got := source.calls.Load(); got != 2 {
		t.Fatalf("load calls = %d, want one in-flight plus one collapsed follow-up", got)
	}
	if got := source.maxActive.Load(); got != 1 {
		t.Fatalf("maximum concurrent loads = %d, want 1", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not stop")
	}
}

func TestRunnerDrainsPollLandingBehindQueuedKick(t *testing.T) {
	store := newCompositionStore(t, "managed")
	source := &blockingSource{
		name:    "managed",
		started: make(chan int, 8),
		release: make(chan struct{}, 8),
		result:  Updated(contributionFor(t, "alice", "sk-alice")),
	}
	runner := newRunner(source, store, 40*time.Millisecond)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		runner.run(ctx)
		close(done)
	}()

	runner.kick()
	waitCall(t, source.started, 1)
	time.Sleep(60 * time.Millisecond)
	runner.kick()
	source.release <- struct{}{}
	waitCall(t, source.started, 2)
	source.release <- struct{}{}
	time.Sleep(20 * time.Millisecond)
	if got := source.calls.Load(); got != 2 {
		t.Fatalf("load calls = %d, want no queued poll behind follow-up kick", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not stop")
	}
}

type resultSource struct {
	name   string
	result LoadResult
	err    error
}

func (s *resultSource) Name() string { return s.name }

func (s *resultSource) Load(context.Context) (LoadResult, error) {
	return s.result, s.err
}

func TestRunnerDispatchesResultsAndHoldsOnInvalid(t *testing.T) {
	store := newCompositionStore(t, "managed")
	initial := contributionFor(t, "initial", "sk-initial")
	if err := store.Install("managed", initial, time.Now()); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		result LoadResult
		err    error
	}{
		{"failure", LoadResult{}, errors.New("unavailable")},
		{"neither arm", LoadResult{}, nil},
		{"both arms", LoadResult{unchanged: true, contribution: contributionFor(t, "new", "sk-new")}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := newRunner(&resultSource{name: "managed", result: tt.result, err: tt.err}, store, time.Hour)
			runner.refresh(t.Context())
			if record, ok := store.Lookup("sk-initial"); !ok || record.Name != "initial" {
				t.Fatalf("last-known-good = %+v, %v", record, ok)
			}
			if _, ok := store.Lookup("sk-new"); ok {
				t.Fatal("invalid result installed its contribution")
			}
		})
	}

	unchanged := newRunner(&resultSource{name: "managed", result: Unchanged()}, store, time.Hour)
	before := store.snapshot.Load().Generation
	unchanged.refresh(t.Context())
	if after := store.snapshot.Load().Generation; after != before {
		t.Fatalf("unchanged refresh generation = %d, want %d", after, before)
	}

	updated := newRunner(&resultSource{name: "managed", result: Updated(contributionFor(t, "new", "sk-new"))}, store, time.Hour)
	updated.refresh(t.Context())
	if _, ok := store.Lookup("sk-initial"); ok {
		t.Fatal("updated refresh retained deleted key")
	}
	if record, ok := store.Lookup("sk-new"); !ok || record.Name != "new" {
		t.Fatalf("updated lookup = %+v, %v", record, ok)
	}
}

func TestRunnerRefreshFailureEscalatesAtTenPollIntervals(t *testing.T) {
	logs := captureKeyLogs(t)
	base := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	clock := &fakeKeyClock{now: base}
	store := newEmptyStore(clock.time)
	if err := store.registerSource(&staticSource{name: "managed"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	store.finishBoot()
	t.Cleanup(func() { store.Close() })
	if err := store.Install("managed", contributionFor(t, "alice", "sk-alice"), base); err != nil {
		t.Fatal(err)
	}
	runner := newRunner(&resultSource{name: "managed", err: errors.New("unavailable")}, store, time.Minute)

	clock.set(base.Add(10*time.Minute - time.Nanosecond))
	runner.refresh(t.Context())
	if text := logs.String(); !strings.Contains(text, "WARNING") || strings.Contains(text, "ERROR") {
		t.Fatalf("pre-threshold failure log = %q, want warning", text)
	}

	logs.Reset()
	clock.set(base.Add(10 * time.Minute))
	runner.refresh(t.Context())
	if text := logs.String(); !strings.Contains(text, "ERROR") {
		t.Fatalf("threshold failure log = %q, want error", text)
	}
}

func TestStoreCloseCancelsInFlightLoadWithoutFailureLog(t *testing.T) {
	logs := captureKeyLogs(t)
	store := newCompositionStore(t, "managed")
	source := &blockingSource{
		name:       "managed",
		started:    make(chan int, 1),
		release:    make(chan struct{}),
		result:     Updated(contributionFor(t, "alice", "sk-alice")),
		respectCtx: true,
	}
	runner := newRunner(source, store, time.Hour)
	store.runners = append(store.runners, runner)
	store.wg.Add(1)
	go func() {
		defer store.wg.Done()
		runner.run(store.ctx)
	}()
	runner.kick()
	waitCall(t, source.started, 1)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "refresh failed") {
		t.Fatalf("shutdown logged a refresh failure: %q", logs.String())
	}
}
