# Version 2 Review (Post-Fixes)

This follow-up covers the improvements implemented after the initial review and highlights any remaining gaps. The focus areas are ingestion buffering, producer context handling, consumer batching, and shutdown safety.

## Improvements Implemented
- **Bounded work queue with observability.** `internal/service/ingestion.go:25` now caps `workCh` at 10k batches (down from 1M), emits a Prometheus gauge (`ingestion_work_queue_depth`) via `internal/metrics/metrics.go:11`, and logs a warning once depth exceeds 90% of capacity. This prevents unbounded memory growth and gives operators a signal when Kafka falls behind.
- **Context-aware producers.** Each producer goroutine derives work from a shared cancellation context plus a 10-second timeout before calling `queue.EnqueueBatch` (`internal/service/ingestion.go:85`). The HTTP handler’s cancellations and app shutdown now propagate to Kafka writes, avoiding infinite hangs when brokers stall.
- **Graceful worker shutdown.** `IngestionService.Close` closes the channel, cancels the producer context, and waits for all workers to finish (`internal/service/ingestion.go:150`). `cmd/bootstrap/app.go:140` now defers this close, eliminating goroutine leaks and preventing writes after the Kafka writer shuts down.
- **Consumer-side batching.** Kafka consumers feed a new `consumerBatchWriter` (`cmd/bootstrap/consumer_batcher.go:13`), which buffers 512 records (or 500 ms) and persists them in bulk with a 5-second timeout. `cmd/bootstrap/app.go:141` wires it in, so file locks are taken per batch instead of per record, restoring v1-level throughput.
- **Backpressure metrics and errors.** When the work queue is full, `ProcessBatch` warns and exposes depth through Prometheus; it also returns a sentinel `ErrIngestionStopped` if the service is already closed, helping callers distinguish between normal and shutdown states.

## Remaining Concerns
1. **Producer retries / DLQ:** Failed Kafka writes still drop batches after logging (`internal/service/ingestion.go:95`). With the shorter buffer, failures surface faster, but adding retries or a replay mechanism is still important.
2. **Queue depth alerting:** The new metric needs dashboards/alerts to be effective. Documenting the warning threshold (90% of capacity) in runbooks would help responders act promptly.
3. **Batcher flush behavior on shutdown:** The batcher forces a final flush (`cmd/bootstrap/consumer_batcher.go:92`), but it currently uses `context.Background()` when `force` is true. If `SaveBatch` blocks on disk, shutdown can still stall; consider switching to a short timeout even during forced flushes.

## Suggested Next Steps
1. Implement retry/backoff for Kafka producer failures and consider a persistent DLQ for batches that never succeed.
2. Add unit/integration tests for the batcher to ensure batches flush at the expected thresholds and during shutdown.
3. Extend `/metrics` with gauges for producer/consumer error counts and batcher flush sizes to give a holistic view of ingestion health.
