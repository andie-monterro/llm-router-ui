package keys

import (
	"time"

	"github.com/michaelquigley/df/dl"
)

type deadlineTimer interface {
	channel() <-chan time.Time
	stop()
}

type deadlineTimerFactory func(time.Duration) deadlineTimer

type systemDeadlineTimer struct {
	timer *time.Timer
}

func newSystemDeadlineTimer(after time.Duration) *systemDeadlineTimer {
	return &systemDeadlineTimer{timer: time.NewTimer(after)}
}

func (t *systemDeadlineTimer) channel() <-chan time.Time {
	return t.timer.C
}

func (t *systemDeadlineTimer) stop() {
	if !t.timer.Stop() {
		select {
		case <-t.timer.C:
		default:
		}
	}
}

// armDeadlineLocked publishes the earliest deadline of any in-service,
// reloadable contribution. the evaluator owns the timer itself so transitions
// never reset a timer concurrently with its receiver.
func (s *Store) armDeadlineLocked() {
	if s.maxStaleness <= 0 || s.booting {
		return
	}

	var earliest time.Time
	for _, source := range s.order {
		state := s.states[source]
		if !state.reloadable || state.excluded || state.contribution == nil || state.loadedAt.IsZero() {
			continue
		}
		deadline := state.loadedAt.Add(s.maxStaleness)
		if earliest.IsZero() || deadline.Before(earliest) {
			earliest = deadline
		}
	}
	s.deadlineAt = earliest
	s.deadlineArmed = !earliest.IsZero()
	s.deadlineVersion++
	select {
	case s.deadlineSignal <- struct{}{}:
	default:
	}
}

func (s *Store) runStalenessEvaluator() {
	var timer deadlineTimer
	var timerC <-chan time.Time
	var appliedVersion uint64
	defer func() {
		if timer != nil {
			timer.stop()
		}
	}()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.deadlineSignal:
			s.mu.Lock()
			deadline, armed, version := s.deadlineAt, s.deadlineArmed, s.deadlineVersion
			s.mu.Unlock()
			if version == appliedVersion {
				continue
			}
			appliedVersion = version
			if timer != nil {
				timer.stop()
				timer = nil
				timerC = nil
			}
			if armed {
				after := deadline.Sub(s.clock())
				if after < 0 {
					after = 0
				}
				timer = s.newTimer(after)
				timerC = timer.channel()
			}
		case <-timerC:
			timer = nil
			timerC = nil
			s.evaluateStaleness()
		}
	}
}

func (s *Store) evaluateStaleness() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock()
	excluded := false
	for _, source := range s.order {
		state := s.states[source]
		if !state.reloadable || state.excluded || state.contribution == nil || state.loadedAt.IsZero() {
			continue
		}
		if now.Before(state.loadedAt.Add(s.maxStaleness)) {
			continue
		}
		state.excluded = true
		excluded = true
		dl.Errorf("key source '%s' exceeded max staleness after %s; excluding its contribution", source, now.Sub(state.loadedAt))
	}

	if excluded {
		s.recompose()
		return
	}
	s.armDeadlineLocked()
}

func (s *Store) sourceStatus(source string) (age time.Duration, loaded, excluded bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, found := s.states[source]
	if !found || state.loadedAt.IsZero() {
		return 0, false, false
	}
	age = s.clock().Sub(state.loadedAt)
	if age < 0 {
		age = 0
	}
	return age, true, state.excluded
}
