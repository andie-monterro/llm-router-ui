package keys

import (
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeKeyClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeKeyClock) time() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeKeyClock) set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

type fakeDeadlineTimer struct {
	mu      sync.Mutex
	c       chan time.Time
	after   time.Duration
	stopped bool
}

func (t *fakeDeadlineTimer) channel() <-chan time.Time {
	return t.c
}

func (t *fakeDeadlineTimer) stop() {
	t.mu.Lock()
	t.stopped = true
	t.mu.Unlock()
}

func (t *fakeDeadlineTimer) fire(at time.Time) {
	t.mu.Lock()
	stopped := t.stopped
	t.mu.Unlock()
	if !stopped {
		t.c <- at
	}
}

type fakeDeadlineTimers struct {
	created chan *fakeDeadlineTimer
}

func newFakeDeadlineTimers() *fakeDeadlineTimers {
	return &fakeDeadlineTimers{created: make(chan *fakeDeadlineTimer, 16)}
}

func (f *fakeDeadlineTimers) new(after time.Duration) deadlineTimer {
	timer := &fakeDeadlineTimer{c: make(chan time.Time, 1), after: after}
	f.created <- timer
	return timer
}

func (f *fakeDeadlineTimers) next(t *testing.T) *fakeDeadlineTimer {
	t.Helper()
	select {
	case timer := <-f.created:
		return timer
	case <-time.After(time.Second):
		t.Fatal("staleness timer was not armed")
		return nil
	}
}

func newStalenessStore(t *testing.T, clock *fakeKeyClock, timers *fakeDeadlineTimers, maxStaleness time.Duration, names ...string) *Store {
	t.Helper()
	store := newEmptyStoreWithTimer(clock.time, timers.new)
	store.maxStaleness = maxStaleness
	for _, name := range names {
		if err := store.registerSource(&staticSource{name: name}, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	store.finishBoot()
	store.start(nil)
	t.Cleanup(func() { store.Close() })
	return store
}

func waitExcluded(t *testing.T, store *Store, source string, want bool) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		store.mu.Lock()
		got := store.states[source].excluded
		store.mu.Unlock()
		if got == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("source %q excluded = %v, want %v", source, got, want)
		default:
			runtime.Gosched()
		}
	}
}

func TestStalenessTimerArmingExclusionAndTouchReinstatement(t *testing.T) {
	logs := captureKeyLogs(t)
	base := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	clock := &fakeKeyClock{now: base}
	timers := newFakeDeadlineTimers()
	store := newStalenessStore(t, clock, timers, 10*time.Minute, "first", "second")

	if err := store.Install("first", contributionFor(t, "alice", "sk-alice"), base); err != nil {
		t.Fatal(err)
	}
	firstTimer := timers.next(t)
	if firstTimer.after != 10*time.Minute {
		t.Fatalf("first timer = %s, want 10m", firstTimer.after)
	}

	clock.set(base.Add(time.Minute))
	if err := store.Install("second", contributionFor(t, "bob", "sk-bob"), clock.time()); err != nil {
		t.Fatal(err)
	}
	firstDeadlineAgain := timers.next(t)
	if firstDeadlineAgain.after != 9*time.Minute {
		t.Fatalf("timer after second install = %s, want 9m", firstDeadlineAgain.after)
	}

	clock.set(base.Add(10 * time.Minute))
	firstDeadlineAgain.fire(clock.time())
	secondTimer := timers.next(t)
	if secondTimer.after != time.Minute {
		t.Fatalf("timer after first exclusion = %s, want 1m", secondTimer.after)
	}
	waitExcluded(t, store, "first", true)
	if _, ok := store.Lookup("sk-alice"); ok {
		t.Fatal("stale source remained in the resident snapshot")
	}
	if _, ok := store.Lookup("sk-bob"); !ok {
		t.Fatal("fresh source was removed with stale source")
	}

	clock.set(base.Add(11 * time.Minute))
	secondTimer.fire(clock.time())
	waitExcluded(t, store, "second", true)
	store.mu.Lock()
	armed := store.deadlineArmed
	store.mu.Unlock()
	if armed {
		t.Fatal("timer remained armed after the last in-service source was excluded")
	}

	clock.set(base.Add(12 * time.Minute))
	if err := store.Touch("first", clock.time()); err != nil {
		t.Fatal(err)
	}
	reinstatedTimer := timers.next(t)
	if reinstatedTimer.after != 10*time.Minute {
		t.Fatalf("timer after Touch reinstatement = %s, want 10m", reinstatedTimer.after)
	}
	if record, ok := store.Lookup("sk-alice"); !ok || record.Name != "alice" {
		t.Fatalf("Touch did not reinstate retained contribution: %+v, %v", record, ok)
	}
	if text := logs.String(); !strings.Contains(text, "first") || !strings.Contains(text, "excluding its contribution") ||
		!strings.Contains(text, "reinstated") || strings.Contains(text, "sk-alice") {
		t.Fatalf("staleness transition logs = %q", text)
	}
}

func TestStalenessTimerRearmsWhenTouchMovesDeadline(t *testing.T) {
	base := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	clock := &fakeKeyClock{now: base}
	timers := newFakeDeadlineTimers()
	store := newStalenessStore(t, clock, timers, 10*time.Minute, "managed")
	if err := store.Install("managed", contributionFor(t, "alice", "sk-alice"), base); err != nil {
		t.Fatal(err)
	}
	_ = timers.next(t)

	clock.set(base.Add(4 * time.Minute))
	if err := store.Touch("managed", clock.time()); err != nil {
		t.Fatal(err)
	}
	timer := timers.next(t)
	if timer.after != 10*time.Minute {
		t.Fatalf("timer after Touch = %s, want 10m", timer.after)
	}
}

func TestConfigContributionIsNeverEvaluatedForStaleness(t *testing.T) {
	base := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	clock := &fakeKeyClock{now: base}
	timers := newFakeDeadlineTimers()
	store := newEmptyStoreWithTimer(clock.time, timers.new)
	store.maxStaleness = time.Minute
	config := newConfigSource([]EntryConfig{{Name: "breakglass", Key: "sk-breakglass"}})
	if err := store.registerSource(config, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.bootLoad(config, true); err != nil {
		t.Fatal(err)
	}
	store.finishBoot()
	store.start(nil)
	t.Cleanup(func() { store.Close() })

	clock.set(base.Add(24 * time.Hour))
	store.evaluateStaleness()
	if _, ok := store.Lookup("sk-breakglass"); !ok {
		t.Fatal("config contribution was excluded for staleness")
	}
}
