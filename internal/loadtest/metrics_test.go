package loadtest

import (
	"testing"
	"time"
)

func TestPercentiles(t *testing.T) {
	m := newSamples(100)
	for i := 1; i <= 100; i++ {
		m.add(time.Duration(i) * time.Millisecond)
	}
	s := m.summary()
	if s.P50MS != 50 || s.P95MS != 95 || s.P99MS != 99 || s.MaxMS != 100 || s.Observations != 100 || s.Retained != 100 || s.Approximate {
		t.Fatalf("nearest-rank percentiles: %+v", s)
	}
}

func TestMetricsSampleBounds(t *testing.T) {
	m := newSamples(8)
	for i := 1; i <= 1000; i++ {
		m.add(time.Duration(i) * time.Millisecond)
	}
	s := m.summary()
	if s.Observations != 1000 || s.Retained != 8 || !s.Approximate || s.SamplePolicy != "uniform reservoir (Algorithm R)" || s.Estimator != "nearest-rank" || s.Precision == "" || s.MaxMS != 1000 {
		t.Fatalf("bounded samples must disclose their population and precision: %+v", s)
	}
	if s.P95MS <= 8 {
		t.Fatalf("late observations must remain eligible for retention: %+v", s)
	}
}

func TestMeasurementWindow(t *testing.T) {
	start := time.Now()
	w := measurementWindow{start: start, end: start.Add(time.Second), intervals: newSamples(10)}
	state := fixtureSnapshot()
	state.ServerTick = 100
	w.observe("game.snapshot", state, start.Add(-time.Millisecond))
	w.observe("game.started", state, start.Add(10*time.Millisecond))
	state.ServerTick = 110
	w.observe("game.snapshot", state, start.Add(100*time.Millisecond))
	state.ServerTick = 116
	w.observe("game.snapshot", state, start.Add(300*time.Millisecond))
	state.ServerTick = 150
	w.observe("game.snapshot", state, start.Add(1100*time.Millisecond))
	if w.snapshots != 2 || w.intervals.summary().Observations != 1 || w.intervals.summary().P50MS != 200 || w.tickHz() != 30 {
		t.Fatalf("setup and tail must not affect same-window tick/arrival observations: %+v, hz=%g", w, w.tickHz())
	}
}
