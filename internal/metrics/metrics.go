package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	logsIngested = promauto.NewCounter(prometheus.CounterOpts{
		Name: "logs_ingested_total",
		Help: "Total number of log entries successfully persisted to storage.",
	})
	ingestErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ingest_errors_total",
		Help: "Total number of errors encountered while ingesting logs.",
	})
	aggregationRuns = promauto.NewCounter(prometheus.CounterOpts{
		Name: "aggregation_runs_total",
		Help: "Total number of aggregation task executions.",
	})
	ingestionQueueDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ingestion_work_queue_depth",
		Help: "Number of batches waiting to be produced to Kafka.",
	})

	collectorsOnce sync.Once
)

// Init registers default Go/process collectors. It is safe to call multiple times.
func Init() {
	collectorsOnce.Do(func() {
		registerCollector(collectors.NewGoCollector())
		registerCollector(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	})
}

func registerCollector(c prometheus.Collector) {
	if err := prometheus.Register(c); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			_ = are.ExistingCollector
			return
		}
		panic(err)
	}
}

// AddLogsIngested increments the successful ingestion counter.
func AddLogsIngested(n int) {
	if n <= 0 {
		return
	}
	logsIngested.Add(float64(n))
}

// IncIngestErrors increments the ingestion error counter.
func IncIngestErrors() {
	ingestErrors.Inc()
}

// IncAggregationRuns increments the aggregation run counter.
func IncAggregationRuns() {
	aggregationRuns.Inc()
}

// SetIngestionQueueDepth records the current producer work queue size.
func SetIngestionQueueDepth(n int) {
	if n < 0 {
		n = 0
	}
	ingestionQueueDepth.Set(float64(n))
}
