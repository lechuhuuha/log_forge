## Implementation Guidelines

Use these guardrails whenever you add or modify code so the project keeps its current structure and patterns.

- **Package boundaries**
  - `cmd/bootstrap`: wire dependencies, load config, start servers/workers. Keep it free of business logic.
  - `internal/domain`: define small interfaces (`LogStore`, `LogQueue`, etc.) and shared models only.
- `service`: orchestration/services that depend on domain interfaces (e.g., ingestion, aggregation).
  - `internal/storage`, `internal/queue`: concrete infrastructure that satisfies domain interfaces.
  - `internal/http`: thin handlers (parse + validate + delegate).
  - `internal/metrics`: counters/gauges; register idempotently.

- **Dependency injection**
  - Constructors accept interfaces and configs, return concrete structs (e.g., `NewIngestionService(store LogStore, queue LogQueue, ...)`).
  - Prefer interface fields on structs; inject implementations from `cmd/bootstrap`.
  - Always accept a `logger.Logger`; default to `logger.NewNop()` if `nil`.
  - Config structs collect tuning knobs; apply defaults in constructors when values are zero.

- **HTTP layer**
  - Parse & validate request payloads (JSON/CSV), normalize to UTC, trim strings.
  - Handle only routing/validation; delegate work to services.
  - Increment metrics on success/failure; return appropriate HTTP codes.

- **Services & workers**
  - Keep mode-aware logic inside services (e.g., `PipelineMode` switch in ingestion).
  - Respect `context.Context` for cancellation/timeouts; avoid goroutine leaks.
  - For background loops, accept a context and stop on `ctx.Done()`; defer cleanup/`Close`.
  - Batch operations where appropriate; emit warnings when approaching capacity/high-water marks.

- **Storage/queue implementations**
  - Implement domain interfaces only; no service-specific knowledge.
  - File storage uses `logs/YYYY-MM-DD/HH.log.json` NDJSON; guard concurrent writers with per-file locks.
  - Queue implementations (Kafka) handle encode/decode, backoff/retry, DLQ writing on failure.

- **Configuration & defaults**
  - CLI config is parsed in `cmd/bootstrap` then merged with optional file config (`config.Load`).
  - Apply sane defaults for missing values (addr, dirs, intervals, queue sizes).
  - Keep version-specific wiring in `cmd/bootstrap` (e.g., switch between direct file writes and Kafka mode).

- **Logging & metrics**
  - Use structured logging (`logger.F("key", value)`); choose log levels consistently.
  - Update metrics counters/gauges around ingest/aggregation paths; guard against duplicate registration.

- **Testing & safety**
  - Add unit tests beside code (`*_test.go`) covering happy paths and error handling.
  - Use temporary dirs/files in tests; avoid network calls unless mocked (`mock_kafka_queue`).
  - Keep new dependencies minimal; prefer stdlib where possible.

- **Extending with new interfaces**
  - Define the interface in `internal/domain` if it’s cross-cutting; otherwise keep it package-local.
  - Add the interface as a field on the consumer struct, accept it in the constructor, and set it in the struct literal.
  - Wire the concrete implementation in `cmd/bootstrap` (or tests) and keep the rest of the code referencing only the interface.
