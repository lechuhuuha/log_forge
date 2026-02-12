package queue

import (
	"context"
	"encoding/json"

	kafka "github.com/segmentio/kafka-go"

	"github.com/lechuhuuha/log_forge/model"
)

// EnqueueBatch encodes and produces the records to Kafka.
func (q *KafkaLogQueue) EnqueueBatch(ctx context.Context, records []model.LogRecord) error {
	if len(records) == 0 {
		return nil
	}
	messages := make([]kafka.Message, len(records))
	for i, rec := range records {
		data, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		// key := []byte(fmt.Sprintf("%s|%d|%s", rec.Path, rec.Timestamp.UnixNano(), rec.UserAgent))
		messages[i] = kafka.Message{
			// Key:   key,
			Value: data,
			Time:  rec.Timestamp,
		}
	}
	return q.writer.WriteMessages(ctx, messages...)
}
