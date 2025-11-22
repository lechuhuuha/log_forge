File-level skip aggregation

- HTTP ingest: `/logs` receives JSON/CSV; handler validates records and hands batches to the ingestion service.
- Ingestion service (direct mode):
  - Uses ModeDirect (no Kafka).
  - Calls `FileLogStore.SaveBatch`, grouping records by hour and appending NDJSON to `logs/YYYY-MM-DD/HH.log.json`.
- Aggregation with file-level skip:
  - Runs on a timer (config: `aggregation.interval`).
  - Walks all `logs/` files, parses their hour, and writes/refreshes summaries in `analytics/YYYY-MM-DD/summary_<HH>.json`.
  - Skips a log file when its summary is newer than the log file (file-level freshness check, not per-line offsets).
- Observability:
  - `/metrics` exposes custom counters (`log_batches_received_total`, `invalid_requests_total`, `logs_ingested_total`, `ingest_errors_total`, `aggregation_runs_total`) plus queue-depth gauge (unused in direct mode) and Go/process metrics.

Typical flow

1) Client POSTs logs -> handler validates -> ingestion service writes NDJSON to hourly files.
2) Aggregator periodically walks stored files -> refreshes per-hour summaries, skipping files whose summaries are already fresher.

Key knobs (flags/env/YAML)

- server: `addr`
- storage: `logsDir`, `analyticsDir`
- aggregation: `interval`
- ingestion (mostly unused in direct mode but present): `queueBufferSize`, `producerWorkers`, `producerWriteTimeout`, `queueHighWaterPercent`
