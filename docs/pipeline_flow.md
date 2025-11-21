Pipeline v2 flow

- HTTP ingest: `/logs` receives JSON/CSV; handler validates records and hands batches to the ingestion service.
- Ingestion service (producers):
  - Builds a buffered channel (config: `ingestion.queueBufferSize`).
  - Spawns producer workers (config: `ingestion.producerWorkers`).
  - Each worker pulls batches from the channel and calls `KafkaLogQueue.EnqueueBatch` with a write timeout (config: `ingestion.producerWriteTimeout`).
  - Warns when the channel is near capacity (config: `ingestion.queueHighWaterPercent`).
- Kafka producer:
  - Turns each record into a Kafka message and writes in batches.
  - Batch knobs are in config `kafka.*`: `batchSize`, `batchTimeout`, `batchBytes`, `compression`, `async`, `requireAllAcks`, plus `brokers`/`topic`.
- Kafka consumers:
  - `kafka.consumers` controls how many consumer goroutines we start (one reader each; Kafka assigns partitions).
  - Each consumer unmarshals the message into a `LogRecord` and hands it to a consumer batch writer.
- Consumer batch writer (to disk):
  - Buffers records in memory.
  - Flush triggers when size hits `consumer.flushSize` or time hits `consumer.flushInterval`.
  - Saves to files via `SaveBatch` with timeout `consumer.persistTimeout`.
- Aggregation:
  - Runs on a timer (config: `aggregation.interval`).
  - Reads stored logs, updates analytics outputs.

Typical flow

1) Client POSTs logs -> handler validates -> pushes batch onto ingestion channel.
2) Producer workers read from the channel -> enqueue to Kafka.
3) Kafka consumers read from topic partitions -> batch writer buffers -> flushes to files.
4) Aggregator periodically processes the files into analytics results.

Key YAML knobs (configs/config.v2.local.yaml)

- ingestion: `queueBufferSize`, `producerWorkers`, `producerWriteTimeout`, `queueHighWaterPercent`
- consumer: `flushSize`, `flushInterval`, `persistTimeout`
- kafka: `brokers`, `topic`, `groupID`, `batchSize`, `batchBytes`, `batchTimeout`, `compression`, `async`, `consumers`, `requireAllAcks`
- aggregation: `interval`
