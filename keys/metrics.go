package keys

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const keyMeterName = "llm-gateway"

// meters owns the key subsystem's instruments and observable callback.
type meters struct {
	staleness    metric.Float64ObservableGauge
	excluded     metric.Int64ObservableGauge
	refresh      metric.Int64Counter
	resident     metric.Int64ObservableGauge
	registration metric.Registration
}

func (s *Store) initMeters() error {
	return s.initMetersWithMeter(otel.Meter(keyMeterName))
}

func (s *Store) initMetersWithMeter(meter metric.Meter) error {
	meters := &meters{}
	var err error
	meters.staleness, err = meter.Float64ObservableGauge(
		"llm_gateway.keys.source.staleness",
		metric.WithDescription("seconds since a reloadable key source last loaded successfully"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return err
	}
	meters.excluded, err = meter.Int64ObservableGauge(
		"llm_gateway.keys.source.excluded",
		metric.WithDescription("whether a reloadable key source is excluded for staleness"),
	)
	if err != nil {
		return err
	}
	meters.refresh, err = meter.Int64Counter(
		"llm_gateway.keys.refresh",
		metric.WithDescription("key source refresh results"),
	)
	if err != nil {
		return err
	}
	meters.resident, err = meter.Int64ObservableGauge(
		"llm_gateway.keys.resident",
		metric.WithDescription("records in the composed resident key snapshot"),
	)
	if err != nil {
		return err
	}
	meters.registration, err = meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		s.mu.Lock()
		defer s.mu.Unlock()

		now := s.clock()
		for _, source := range s.order {
			state := s.states[source]
			if !state.reloadable || state.loadedAt.IsZero() {
				continue
			}
			age := now.Sub(state.loadedAt).Seconds()
			if age < 0 {
				age = 0
			}
			attrs := metric.WithAttributes(attribute.String("source", source))
			observer.ObserveFloat64(meters.staleness, age, attrs)
			observer.ObserveInt64(meters.excluded, boolInt64(state.excluded), attrs)
		}

		resident := int64(0)
		if snapshot := s.snapshot.Load(); snapshot != nil {
			resident = int64(len(snapshot.byDigest))
		}
		observer.ObserveInt64(meters.resident, resident)
		return nil
	}, meters.staleness, meters.excluded, meters.resident)
	if err != nil {
		return err
	}
	s.meters = meters
	return nil
}

func boolInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func (s *Store) recordRefresh(ctx context.Context, source, result string) {
	if s.meters == nil {
		return
	}
	s.meters.refresh.Add(ctx, 1, metric.WithAttributes(
		attribute.String("source", source),
		attribute.String("result", result),
	))
}

func (m *meters) close() error {
	if m == nil || m.registration == nil {
		return nil
	}
	if err := m.registration.Unregister(); err != nil {
		return fmt.Errorf("unregister API key metric callback: %w", err)
	}
	return nil
}
