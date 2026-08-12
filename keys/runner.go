package keys

import (
	"context"
	"fmt"
	"time"

	"github.com/michaelquigley/df/dl"
)

type runner struct {
	source   Source
	store    *Store
	interval time.Duration
	trigger  chan struct{}
}

func newRunner(source Source, store *Store, interval time.Duration) *runner {
	return &runner{
		source:   source,
		store:    store,
		interval: interval,
		trigger:  make(chan struct{}, 1),
	}
}

func (r *runner) run(ctx context.Context) {
	timer := time.NewTimer(r.interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			r.refresh(ctx)
		case <-r.trigger:
			r.refresh(ctx)
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(r.interval)
	}
}

func (r *runner) kick() {
	select {
	case r.trigger <- struct{}{}:
	default:
	}
}

func (r *runner) refresh(ctx context.Context) {
	result, err := r.source.Load(ctx)
	if err == nil {
		if validationErr := result.validate(); validationErr != nil {
			err = fmt.Errorf("source bug: %w", validationErr)
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		r.store.recordRefresh(ctx, r.source.Name(), "failure")
		r.logFailure(err)
		return
	}

	now := r.store.clock()
	if result.IsUnchanged() {
		if err := r.store.Touch(r.source.Name(), now); err != nil {
			r.store.recordRefresh(ctx, r.source.Name(), "failure")
			r.logFailure(err)
			return
		}
		r.store.recordRefresh(ctx, r.source.Name(), "not_modified")
		return
	}
	if err := r.store.Install(r.source.Name(), result.Contribution(), now); err != nil {
		r.store.recordRefresh(ctx, r.source.Name(), "failure")
		r.logFailure(err)
		return
	}
	r.store.recordRefresh(ctx, r.source.Name(), "success")
}

func (r *runner) logFailure(err error) {
	age, loaded, excluded := r.store.sourceStatus(r.source.Name())
	if !loaded {
		dl.Errorf("key source '%s' refresh failed and has never loaded successfully; no last-known-good contribution is available: %v", r.source.Name(), err)
		return
	}
	retention := "holding last-known-good"
	if excluded {
		retention = "retained contribution remains excluded"
	}
	if r.interval > 0 && age/r.interval >= 10 {
		dl.Errorf("key source '%s' refresh failed; last successful load was %s ago; %s: %v", r.source.Name(), age, retention, err)
		return
	}
	dl.Warnf("key source '%s' refresh failed; last successful load was %s ago; %s: %v", r.source.Name(), age, retention, err)
}
