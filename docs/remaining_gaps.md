# Remaining Gaps

## Open (not fixed yet)

- **Shutdown flush can hang.** `service/consumer.go` switches to `context.Background()` on forced flush, so a stuck `SaveBatch` can block shutdown indefinitely. Should use a bounded timeout even during forced flush.

## Addressed (verified)

- **Producer drops batches on Kafka write errors.** Producers now retry enqueues with backoff and, after the final failure, write batches to a producer DLQ instead of discarding them (`service/producer.go`). DLQ defaults to `dlq/producer`, configurable via `ProducerConfig`.
- **Kafka consumer commits gated on persistence with DLQ.** Consumers fetch without auto-commit, commit after successful `SaveBatch`, and write DLQ entries on failure (`internal/queue/kafka_queue.go`, `service/consumer.go`).
- **Analytics output no longer collides across days.** Summaries now include the date in the path (`service/aggregation.go` -> `analytics/YYYY-MM-DD/summary_<HH>.json`).
- **Aggregator processes all stored hours with freshness checks.** `AggregateAll` walks `logs/` and skips files only when the summary is newer (`service/aggregation.go`), covering late or prior-hour data.
- **Observability counters added.** Batch and invalid-request counters were introduced and wired (`internal/metrics/metrics.go`, `internal/http/handler.go`, `service/ingestion.go`), filling the earlier gap called out in the challenge.
- **/logs parsing/validation now covered by tests.** Added `internal/http/handler_test.go` to exercise JSON and CSV decoding success paths, validation failures (missing fields, malformed payloads, bad timestamps), metric increments for invalid requests, and propagation of ingestion errors.
