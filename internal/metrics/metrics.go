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
	batchesReceived = promauto.NewCounter(prometheus.CounterOpts{
		Name: "log_batches_received_total",
		Help: "Total number of log batches accepted by the API.",
	})
	ingestErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ingest_errors_total",
		Help: "Total number of errors encountered while ingesting logs.",
	})
	invalidRequests = promauto.NewCounter(prometheus.CounterOpts{
		Name: "invalid_requests_total",
		Help: "Total number of invalid /logs requests rejected during parsing or validation.",
	})
	aggregationRuns = promauto.NewCounter(prometheus.CounterOpts{
		Name: "aggregation_runs_total",
		Help: "Total number of aggregation task executions.",
	})
	ingestionQueueDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ingestion_work_queue_depth",
		Help: "Number of batches waiting to be produced to Kafka.",
	})
	kafkaUp = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "kafka_up",
		Help: "Whether Kafka is reachable by the application (1=up, 0=down).",
	})
	kafkaConsumerErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "kafka_consumer_errors_total",
		Help: "Total number of Kafka consumer fetch/connect errors.",
	})
	kafkaWriterErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "kafka_writer_errors_total",
		Help: "Total number of Kafka writer internal errors.",
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

// IncBatchesReceived increments the batch counter.
func IncBatchesReceived() {
	batchesReceived.Inc()
}

// IncIngestErrors increments the ingestion error counter.
func IncIngestErrors() {
	ingestErrors.Inc()
}

// IncInvalidRequests increments the invalid request counter.
func IncInvalidRequests() {
	invalidRequests.Inc()
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

// SetKafkaUp records whether Kafka is currently reachable.
func SetKafkaUp(up bool) {
	if up {
		kafkaUp.Set(1)
		return
	}
	kafkaUp.Set(0)
}

// IncKafkaConsumerErrors increments Kafka consumer error counter.
func IncKafkaConsumerErrors() {
	kafkaConsumerErrors.Inc()
}

// IncKafkaWriterErrors increments Kafka writer error counter.
func IncKafkaWriterErrors() {
	kafkaWriterErrors.Inc()
}
