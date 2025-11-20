# Version 2 Log Handling Review

## Scope
The current Version 2 (v2) pipeline accepts `/logs` batches, enqueues them onto a buffered channel, produces batches to Kafka, and relies on background consumers to persist each message to the NDJSON file store before aggregation picks it up. This document highlights how the latest v2 implementation compares to Version 1 (synchronous file writes) and to the previous v2 design that produced directly from the HTTP handler without a work queue. Where older artifacts are unavailable, the comparison relies on the behavior captured in the repository at the time of this review.

## Improvements Over Version 1
- **Async isolation between HTTP and storage** – `internal/service/ingestion.go:38-97` buffers requests (1M slots) and acknowledges once the batch enters the queue, so `/logs` latency is dominated by validation, not disk I/O. File writes now happen in Kafka consumers, so spikes in aggregation or disk flush do not back up the HTTP handler.
- **Durable fan-out via Kafka** – `cmd/bootstrap/app.go:69-128` wires v2 to a configurable Kafka cluster. Producers honor caller-provided `requireAllAcks`, `batchSize`, and `batchTimeout`, yielding better durability and throughput than v1’s single file mutex.
- **Horizontal scaling knobs** – `internal/queue/kafka_queue.go:120-170` starts `consumers` goroutines that share a consumer group. With a multi-partition topic, the persistence stage scales beyond the single-file lock that v1 was constrained to.
- **Backpressure instead of synchronous blocking** – the buffered `workCh` lets the server absorb short bursts faster than v1’s direct `SaveBatch`, which blocked every handler until the file lock released (`internal/service/ingestion.go:38-55` vs. `internal/storage/file_store.go:41-82`).
- **Instrumentation hooks** – v2 keeps the existing Prometheus counters and adds structured logs for producer workers (`internal/service/ingestion.go:64-75`) and Kafka consumers (`internal/queue/kafka_queue.go:129-170`), making it easier to trace which stage is saturated.

## Improvements Over the Previous v2 Iteration
- **Dedicated producer workers** – Earlier v2 produced to Kafka straight from the handler, so each HTTP goroutine paid the Kafka round-trip. The new worker pool (`internal/service/ingestion.go:38-75`) fan-outs batches to long-lived producers, reducing connection churn and balancing load across requests.
- **Configurable batching semantics** – `configs/config.v2.local.yaml` now sets aggressive Kafka batch sizes (`batchSize: 50000`, `batchTimeout: 200ms`) and 10 consumers, improving throughput compared with the earlier defaults that mirrored v1.
- **Consumer-side observability** – Logging of partition, offset, and active consumer count in `internal/queue/kafka_queue.go:154-169` replaces the old “best effort” stdout prints, which simplifies diagnosing partition assignments and lag.

## Findings / Regressions
1. **Batches drop on producer errors without retry** – `internal/service/ingestion.go:64-70` logs Kafka write failures and increments `ingest_errors_total` but immediately discards the batch. v1 never acknowledged the request unless disk writes succeeded, so data loss was hard to miss. In v2 the client already received `202 Accepted`, so failures silently lose data unless you tail the logs.
2. **Producer goroutines ignore request cancellation** – When Kafka slows down, `runProducer` calls `EnqueueBatch` with `context.Background()` (`internal/service/ingestion.go:64-67`), so timeouts and shutdown signals do not interrupt stuck writes. This can deadlock the workers and prevents graceful shutdown. Passing the HTTP context (or a bounded producer context) would give you the same cancellation semantics the handler already enforces.
3. **Queue buffer can exhaust memory under sustained load** – The work queue holds up to one million batches (`internal/service/ingestion.go:38-44`). Each batch retains the JSON slices received over HTTP, so a burst of large requests can pin several gigabytes even if Kafka is unavailable. Nothing drains the channel when the app exits, so these buffers linger until process death.
4. **Persisting one record at a time regresses write amplification** – Consumers call `store.SaveBatch` with slices of length one (`cmd/bootstrap/app.go:96-111`), which forces `internal/storage/file_store.go:66-96` to open+close files for every message. V1 wrote the entire HTTP batch per file lock, so file descriptors and system calls were amortized. With high throughput, v2 may spend more time thrashing file locks than producing to Kafka.
5. **Channel workers never shut down** – `workCh` is never closed (`internal/service/ingestion.go:43-75`), so the 100 producer goroutines leak until process exit. During shutdown, they continue to block on `<-s.workCh` while the Kafka writer closes underneath them, which risks panics if a batch slips through late.
6. **API semantics diverge from v1 with no client signal** – `/logs` still returns `202 Accepted` immediately (`internal/http/handler.go:31-51`), but in v1 this meant “data persisted,” whereas in v2 it only means “batch staged for Kafka.” Without surfacing offsets or a queue depth metric, clients cannot tell whether their data survives.

## Suggested Follow-Ups
- Add a retry / DLQ path for failed Kafka writes, or at least keep the batch in memory until Kafka becomes available again.
- Replace `context.Background()` with a producer-scoped context derived from the handler context plus a timeout.
- Make `workQueueSize` configurable and measure typical batch sizes to rightsize the buffer.
- Batch multiple Kafka messages before calling `SaveBatch` (e.g., pull `[]domain.LogRecord` off the consumer handler or accumulate per partition) to regain the throughput v1 enjoyed.
- Close `workCh` when shutting down and wait for producer goroutines to finish to avoid writing to a closed Kafka writer.
- Expose metrics such as queue depth, producer error counts, and Kafka lag so clients and operators can validate that v2 is actually ingesting.
