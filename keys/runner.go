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
		dl.Warnf("key source '%s' refresh failed; holding last-known-good: %v", r.source.Name(), err)
		return
	}

	now := r.store.clock()
	if result.IsUnchanged() {
		if err := r.store.Touch(r.source.Name(), now); err != nil {
			dl.Warnf("key source '%s' refresh failed; holding last-known-good: %v", r.source.Name(), err)
		}
		return
	}
	if err := r.store.Install(r.source.Name(), result.Contribution(), now); err != nil {
		dl.Warnf("key source '%s' refresh failed; holding last-known-good: %v", r.source.Name(), err)
	}
}
