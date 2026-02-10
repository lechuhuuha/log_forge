package metrics

import (
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestCounters(t *testing.T) {
	Init()

	cases := []struct {
		name   string
		metric string
		inc    func()
		delta  float64
	}{
		{
			name:   "batches received",
			metric: "log_batches_received_total",
			inc:    IncBatchesReceived,
			delta:  1,
		},
		{
			name:   "ingest errors",
			metric: "ingest_errors_total",
			inc:    IncIngestErrors,
			delta:  1,
		},
		{
			name:   "invalid requests",
			metric: "invalid_requests_total",
			inc:    IncInvalidRequests,
			delta:  1,
		},
		{
			name:   "aggregation runs",
			metric: "aggregation_runs_total",
			inc:    IncAggregationRuns,
			delta:  1,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			start := counterValue(t, tc.metric)
			tc.inc()
			got := counterValue(t, tc.metric)
			if got != start+tc.delta {
				t.Fatalf("unexpected counter delta: metric=%s got=%v start=%v", tc.metric, got, start)
			}
		})
	}

	t.Run("add logs ingested", func(t *testing.T) {
		start := counterValue(t, "logs_ingested_total")
		calls := []int{0, -1, 3}
		for _, n := range calls {
			AddLogsIngested(n)
		}
		got := counterValue(t, "logs_ingested_total")
		if got != start+3 {
			t.Fatalf("unexpected logs ingested delta: got=%v start=%v", got, start)
		}
	})
}

func TestSetIngestionQueueDepth(t *testing.T) {
	cases := []struct {
		name  string
		value int
		want  float64
	}{
		{name: "positive", value: 7, want: 7},
		{name: "negative clamps to zero", value: -1, want: 0},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			SetIngestionQueueDepth(tc.value)
			if got := gaugeValue(t, "ingestion_work_queue_depth"); got != tc.want {
				t.Fatalf("unexpected queue depth: got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestRegisterCollector(t *testing.T) {
	name := fmt.Sprintf("test_metrics_collector_%d", time.Now().UnixNano())
	g := prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: "test"})

	cases := []struct {
		name string
		call func()
	}{
		{name: "first registration", call: func() { registerCollector(g) }},
		{name: "already registered", call: func() { registerCollector(g) }},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tc.call()
		})
	}
}

func counterValue(t *testing.T, name string) float64 {
	t.Helper()
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() == name && len(mf.Metric) > 0 && mf.Metric[0].Counter != nil {
			return mf.Metric[0].Counter.GetValue()
		}
	}
	return 0
}

func gaugeValue(t *testing.T, name string) float64 {
	t.Helper()
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() == name && len(mf.Metric) > 0 && mf.Metric[0].Gauge != nil {
			return mf.Metric[0].Gauge.GetValue()
		}
	}
	return 0
}
