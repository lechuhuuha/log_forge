# Remaining Gaps

## Open (not fixed yet)

- **Shutdown flush can hang.** `cmd/bootstrap/consumer_batcher.go:123-165` switches to `context.Background()` on forced flush, so a stuck `SaveBatch` can block shutdown indefinitely. Should use a bounded timeout even during forced flush.

## Addressed (verified)

- **Producer drops batches on Kafka write errors.** Producers now retry enqueues with backoff and, after the final failure, write batches to a producer DLQ instead of discarding them (`internal/service/ingestion.go:40-214`). DLQ defaults to `dlq/producer`, configurable via `IngestionConfig`.
- **Kafka consumer commits gated on persistence with DLQ.** Consumers fetch without auto-commit, commit after successful `SaveBatch`, and write DLQ entries on failure (`internal/queue/kafka_queue.go:146-202`, `cmd/bootstrap/consumer_batcher.go:20-191`).
- **Analytics output no longer collides across days.** Summaries now include the date in the path (`internal/service/aggregation.go:128-169` -> `analytics/YYYY-MM-DD/summary_<HH>.json`).
- **Aggregator processes all stored hours with freshness checks.** `AggregateAll` walks `logs/` and skips files only when the summary is newer (`internal/service/aggregation.go:53-135`), covering late or prior-hour data.
- **Observability counters added.** Batch and invalid-request counters were introduced and wired (`internal/metrics/metrics.go:22-64`, `internal/http/handler.go:23-74`, `internal/service/ingestion.go:141-190`), filling the earlier gap called out in the challenge.
- **/logs parsing/validation now covered by tests.** Added `internal/http/handler_test.go` to exercise JSON and CSV decoding success paths, validation failures (missing fields, malformed payloads, bad timestamps), metric increments for invalid requests, and propagation of ingestion errors.
