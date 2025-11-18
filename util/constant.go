package util

import "time"

const (
	HTTPAddr            = "HTTP_ADDR"
	PipelineVersion     = "PIPELINE_VERSION"
	LogsDir             = "LOGS_DIR"
	AnalyticsDir        = "ANALYTICS_DIR"
	AggregationInterval = "AGGREGATION_INTERVAL"
	ProfileDir          = "PROFILE_DIR"
	ProfileName         = "PROFILE_NAME"
	ProfileAddr         = "PROFILE_ADDR"
	ProfileEnable       = "PROFILE_ENABLED"
	ProfileCapture      = "PROFILE_CAPTURE"
	KafkaBrokers        = "KAFKA_BROKERS"
	KafkaTopic          = "KAFKA_TOPIC"
	KafkaGroupID        = "KAFKA_GROUP_ID"
	KafkaBatchSize      = "KAFKA_BATCH_SIZE"
	KafkaBatchTimeout   = "KAFKA_BATCH_TIMEOUT"
	KafkaConsumers      = "KAFKA_CONSUMERS"
	KafkaAcks           = "KAFKA_ACKS"
)

const (
	DefaultHTTPAddr            = ":8082"
	DefaultPipelineVersion     = 1
	DefaultLogsDir             = "logs"
	DefaultAnalyticsDir        = "analytics"
	DefaultAggregationInterval = time.Minute
	DefaultProfileDir          = "profiles"
	DefaultProfileAddr         = ":6060"
	DefaultKafkaTopic          = "logs"
	DefaultKafkaGroupID        = "logs-consumer-group"
	DefaultKafkaBatchSize      = 100
	DefaultKafkaBatchTimeout   = time.Second
	DefaultKafkaConsumers      = 1
)
