# Remaining Gaps

## Open (not fixed yet)

- **Kafka consumer commits before persistence.** `internal/queue/kafka_queue.go:167-201` uses `ReadMessage`, which auto-acknowledges offsets before the handler writes to disk. If `consumer_batcher` fails to persist, the Kafka message is already committed and is lost. Needs manual commit after successful `SaveBatch` or a retry/DLQ path.
- **Producer drops batches on Kafka write errors.** `internal/service/ingestion.go:121-138` logs and increments `ingest_errors_total` when `EnqueueBatch` fails, then discards the batch even though the client already got `202 Accepted`. There is no retry/backoff or DLQ, so producer-side failures still lose data.
- **Shutdown flush can hang.** `cmd/bootstrap/consumer_batcher.go:123-165` switches to `context.Background()` on forced flush, so a stuck `SaveBatch` can block shutdown indefinitely. Should use a bounded timeout even during forced flush.

## Addressed (verified)

- **Analytics output no longer collides across days.** Summaries now include the date in the path (`internal/service/aggregation.go:128-169` -> `analytics/YYYY-MM-DD/summary_<HH>.json`).
- **Aggregator processes all stored hours with freshness checks.** `AggregateAll` walks `logs/` and skips files only when the summary is newer (`internal/service/aggregation.go:53-135`), covering late or prior-hour data.
- **Observability counters added.** Batch and invalid-request counters were introduced and wired (`internal/metrics/metrics.go:22-64`, `internal/http/handler.go:23-74`, `internal/service/ingestion.go:141-190`), filling the earlier gap called out in the challenge.
- **/logs parsing/validation now covered by tests.** Added `internal/http/handler_test.go` to exercise JSON and CSV decoding success paths, validation failures (missing fields, malformed payloads, bad timestamps), metric increments for invalid requests, and propagation of ingestion errors.
