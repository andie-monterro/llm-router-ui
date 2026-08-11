package keys

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func newMetricReader(t *testing.T, store *Store) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	if err := store.initMetersWithMeter(provider.Meter(keyMeterName)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("meter provider shutdown: %v", err)
		}
	})
	return reader
}

func collectKeyMetrics(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Aggregation {
	t.Helper()
	var resource metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &resource); err != nil {
		t.Fatal(err)
	}
	result := make(map[string]metricdata.Aggregation)
	for _, scope := range resource.ScopeMetrics {
		for _, item := range scope.Metrics {
			result[item.Name] = item.Data
		}
	}
	return result
}

func pointSource(attributes attribute.Set) string {
	value, found := attributes.Value(attribute.Key("source"))
	if !found {
		return ""
	}
	return value.AsString()
}

func TestKeyObservableMetricsTrackAgeExclusionAndResidentRecords(t *testing.T) {
	base := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	clock := &fakeKeyClock{now: base}
	store := newEmptyStore(clock.time)
	reader := newMetricReader(t, store)

	for _, item := range []struct {
		name     string
		interval time.Duration
	}{{configSourceName, 0}, {"never-loaded", time.Minute}, {"managed", time.Minute}} {
		if err := store.registerSource(&staticSource{name: item.name}, item.interval); err != nil {
			t.Fatal(err)
		}
	}
	store.finishBoot()
	if err := store.Install(configSourceName, contributionFor(t, "breakglass", "sk-breakglass"), base); err != nil {
		t.Fatal(err)
	}
	if err := store.Install("managed", contributionFor(t, "alice", "sk-alice"), base); err != nil {
		t.Fatal(err)
	}

	clock.set(base.Add(5 * time.Minute))
	metrics := collectKeyMetrics(t, reader)
	staleness, ok := metrics["llm_gateway.keys.source.staleness"].(metricdata.Gauge[float64])
	if !ok || len(staleness.DataPoints) != 1 || pointSource(staleness.DataPoints[0].Attributes) != "managed" || staleness.DataPoints[0].Value != 300 {
		t.Fatalf("staleness metric = %#v, want managed=300 only", metrics["llm_gateway.keys.source.staleness"])
	}
	excluded, ok := metrics["llm_gateway.keys.source.excluded"].(metricdata.Gauge[int64])
	if !ok || len(excluded.DataPoints) != 1 || pointSource(excluded.DataPoints[0].Attributes) != "managed" || excluded.DataPoints[0].Value != 0 {
		t.Fatalf("excluded metric = %#v, want managed=0 only", metrics["llm_gateway.keys.source.excluded"])
	}
	resident, ok := metrics["llm_gateway.keys.resident"].(metricdata.Gauge[int64])
	if !ok || len(resident.DataPoints) != 1 || resident.DataPoints[0].Value != 2 {
		t.Fatalf("resident metric = %#v, want 2", metrics["llm_gateway.keys.resident"])
	}

	clock.set(base.Add(8 * time.Minute))
	metrics = collectKeyMetrics(t, reader)
	staleness = metrics["llm_gateway.keys.source.staleness"].(metricdata.Gauge[float64])
	if staleness.DataPoints[0].Value != 480 {
		t.Fatalf("staleness after clock advance = %v, want 480", staleness.DataPoints[0].Value)
	}

	if err := store.Install("never-loaded", contributionFor(t, "bob", "sk-bob"), clock.time()); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.states["managed"].excluded = true
	store.recompose()
	store.mu.Unlock()
	metrics = collectKeyMetrics(t, reader)
	excluded = metrics["llm_gateway.keys.source.excluded"].(metricdata.Gauge[int64])
	if len(excluded.DataPoints) != 2 {
		t.Fatalf("excluded series count = %d, want 2 after first optional load", len(excluded.DataPoints))
	}
	values := map[string]int64{}
	for _, point := range excluded.DataPoints {
		values[pointSource(point.Attributes)] = point.Value
	}
	if values["managed"] != 1 || values["never-loaded"] != 0 {
		t.Fatalf("excluded values = %#v", values)
	}
	resident = metrics["llm_gateway.keys.resident"].(metricdata.Gauge[int64])
	if resident.DataPoints[0].Value != 2 {
		t.Fatalf("resident after exclusion = %d, want config plus never-loaded source", resident.DataPoints[0].Value)
	}
}

func TestKeyRefreshCounterClassifiesEveryResult(t *testing.T) {
	store := newCompositionStore(t, "managed")
	reader := newMetricReader(t, store)
	if err := store.Install("managed", contributionFor(t, "initial", "sk-initial"), time.Now()); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		result LoadResult
		err    error
	}{
		{err: errors.New("unavailable")},
		{},
		{result: LoadResult{unchanged: true, contribution: contributionFor(t, "bad", "sk-bad")}},
		{result: Unchanged()},
		{result: Updated(contributionFor(t, "updated", "sk-updated"))},
	}
	for _, test := range tests {
		newRunner(&resultSource{name: "managed", result: test.result, err: test.err}, store, time.Hour).refresh(t.Context())
	}

	metrics := collectKeyMetrics(t, reader)
	refresh, ok := metrics["llm_gateway.keys.refresh"].(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("refresh metric = %#v, want int64 sum", metrics["llm_gateway.keys.refresh"])
	}
	values := map[string]int64{}
	for _, point := range refresh.DataPoints {
		result, _ := point.Attributes.Value(attribute.Key("result"))
		source, _ := point.Attributes.Value(attribute.Key("source"))
		if source.AsString() != "managed" {
			t.Fatalf("refresh source = %q, want managed", source.AsString())
		}
		values[result.AsString()] = point.Value
	}
	if values["success"] != 1 || values["not_modified"] != 1 || values["failure"] != 3 {
		t.Fatalf("refresh results = %#v, want success=1 not_modified=1 failure=3", values)
	}
}

func TestShutdownCancellationDoesNotIncrementRefreshFailure(t *testing.T) {
	store := newCompositionStore(t, "managed")
	reader := newMetricReader(t, store)
	source := &blockingSource{
		name:       "managed",
		started:    make(chan int, 1),
		release:    make(chan struct{}),
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

	metrics := collectKeyMetrics(t, reader)
	refresh, found := metrics["llm_gateway.keys.refresh"]
	if !found {
		return
	}
	for _, point := range refresh.(metricdata.Sum[int64]).DataPoints {
		result, _ := point.Attributes.Value(attribute.Key("result"))
		if result.AsString() == "failure" && point.Value != 0 {
			t.Fatalf("shutdown cancellation recorded %d refresh failures", point.Value)
		}
	}
}
