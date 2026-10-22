package loadtest

import (
	"math"
	"math/rand"
	"sort"
	"time"

	"github.com/sangjinsu/coopdock/internal/room"
)

// Distribution describes all observations, including ACKs received during drain.
type Distribution struct {
	Observations uint64  `json:"observations"`
	Retained     int     `json:"retained"`
	SampleLimit  int     `json:"sample_limit"`
	SamplePolicy string  `json:"sample_policy"`
	Estimator    string  `json:"estimator"`
	Precision    string  `json:"precision"`
	Approximate  bool    `json:"approximate"`
	P50MS        float64 `json:"p50_ms"`
	P95MS        float64 `json:"p95_ms"`
	P99MS        float64 `json:"p99_ms"`
	MaxMS        float64 `json:"max_ms"`
}

type samples struct {
	limit   int
	count   uint64
	values  []time.Duration
	maximum time.Duration
	random  *rand.Rand
}

func newSamples(limit int) *samples {
	return &samples{limit: limit, random: rand.New(rand.NewSource(1))}
}

// Algorithm R gives every observation the same retention probability.
func (s *samples) add(value time.Duration) {
	s.count++
	if value > s.maximum {
		s.maximum = value
	}
	if len(s.values) < s.limit {
		s.values = append(s.values, value)
	} else if index := s.random.Int63n(int64(s.count)); index < int64(s.limit) {
		s.values[index] = value
	}
}

func (s *samples) summary() Distribution {
	values := append([]time.Duration(nil), s.values...)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	quantile := func(p float64) float64 {
		if len(values) == 0 {
			return 0
		}
		return float64(values[int(math.Ceil(p*float64(len(values))))-1]) / float64(time.Millisecond)
	}
	precision := "exact observed quantiles and maximum"
	approximate := s.count > uint64(len(values))
	if approximate {
		precision = "sample quantiles have no fixed error guarantee; maximum covers every observation"
	}
	return Distribution{Observations: s.count, Retained: len(values), SampleLimit: s.limit,
		SamplePolicy: "uniform reservoir (Algorithm R)", Estimator: "nearest-rank", Precision: precision, Approximate: approximate,
		P50MS: quantile(.50), P95MS: quantile(.95), P99MS: quantile(.99), MaxMS: float64(s.maximum) / float64(time.Millisecond)}
}

type measurementWindow struct {
	start, end                        time.Time
	firstAt, lastAt                   time.Time
	firstTick, lastTick, lastSequence uint64
	intervals                         *samples
	snapshots                         uint64
}

func (w *measurementWindow) observe(op string, s room.Snapshot, at time.Time) (time.Duration, uint64, bool) {
	if op != "game.snapshot" || w.start.IsZero() || at.Before(w.start) || !at.Before(w.end) {
		return 0, 0, false
	}
	var interval time.Duration
	var gap uint64
	if !w.lastAt.IsZero() {
		interval = at.Sub(w.lastAt)
		w.intervals.add(interval)
		if s.SnapshotSeq > w.lastSequence+1 {
			gap = s.SnapshotSeq - w.lastSequence - 1
		}
	} else {
		w.firstAt, w.firstTick = at, s.ServerTick
	}
	w.lastAt, w.lastTick, w.lastSequence = at, s.ServerTick, s.SnapshotSeq
	w.snapshots++
	return interval, gap, true
}
func (w *measurementWindow) tickSeconds() float64 {
	if w.snapshots < 2 {
		return 0
	}
	return w.lastAt.Sub(w.firstAt).Seconds()
}
func (w *measurementWindow) tickDelta() uint64 {
	if w.snapshots < 2 || w.lastTick < w.firstTick {
		return 0
	}
	return w.lastTick - w.firstTick
}
func (w *measurementWindow) tickHz() float64 {
	if w.tickSeconds() <= 0 {
		return 0
	}
	return float64(w.tickDelta()) / w.tickSeconds()
}
