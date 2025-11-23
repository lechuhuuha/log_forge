Pipeline v2 flow

- HTTP ingest: `/logs` receives JSON/CSV; handler validates records and hands batches to the ingestion service.
- Ingestion service (producers):
  - Uses a `ProducerService` with a buffered channel (config: `ingestion.queueBufferSize`).
  - Spawns producer workers (config: `ingestion.producerWorkers`).
  - Each worker pulls batches from the channel and calls `KafkaLogQueue.EnqueueBatch` with retries and a write timeout (config: `ingestion.producerWriteTimeout`, `ingestion.producerMaxRetries`, `ingestion.producerRetryBackoff`).
  - Warns when the channel is near capacity (config: `ingestion.queueHighWaterPercent`).
  - On repeated failures, writes batches to a producer DLQ (`ingestion.producerDLQDir`).
- Kafka producer:
  - Turns each record into a Kafka message and writes in batches.
  - Batch knobs are in config `kafka.*`: `batchSize`, `batchTimeout`, `batchBytes`, `compression`, `async`, `requireAllAcks`, plus `brokers`/`topic`.
- Kafka consumers:
  - `kafka.consumers` controls how many consumer goroutines we start (one reader each; Kafka assigns partitions).
  - Each consumer fetches without auto-commit, unmarshals to `LogRecord`, and passes along a commit hook to the consumer batch writer.
- Consumer service + batch writer (to repo):
  - Buffers records in memory with commit metadata.
  - Flush triggers when size hits `consumer.flushSize` or time hits `consumer.flushInterval`.
  - Saves to files via the repo `SaveBatch` with timeout `consumer.persistTimeout`; after success it commits offsets.
  - On persist failure, writes a DLQ file with partition/offset info (`consumer.dlqDir`) and leaves messages uncommitted for retry.
- Aggregation:
  - Runs on a timer (config: `aggregation.interval`).
  - Reads stored logs, updates analytics outputs.

Typical flow

1) Client POSTs logs -> handler validates -> pushes batch onto ingestion channel.
2) Producer workers read from the channel -> enqueue to Kafka.
3) Kafka consumers read from topic partitions -> batch writer buffers -> flushes to files.
4) Aggregator periodically processes the files into analytics results.

Key YAML knobs (config/examples/config.v2.local.yaml)

- ingestion: `queueBufferSize`, `producerWorkers`, `producerWriteTimeout`, `producerMaxRetries`, `producerRetryBackoff`, `queueHighWaterPercent`, `producerDLQDir`
- consumer: `flushSize`, `flushInterval`, `persistTimeout`, `dlqDir`
- kafka: `brokers`, `topic`, `groupID`, `batchSize`, `batchBytes`, `batchTimeout`, `compression`, `async`, `consumers`, `requireAllAcks`
- aggregation: `interval`
