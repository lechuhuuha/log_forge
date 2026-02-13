package queue

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"sync/atomic"
	"time"

	loggerpkg "github.com/lechuhuuha/log_forge/logger"
	"github.com/lechuhuuha/log_forge/model"
	"github.com/lechuhuuha/log_forge/util"
)

// StartConsumers spawns background consumer goroutines.
func (q *KafkaLogQueue) StartConsumers(ctx context.Context, handler func(context.Context, model.ConsumedMessage)) error {
	if handler == nil {
		return errors.New("handler required")
	}
	if q.readerFactory == nil {
		q.readerFactory = defaultKafkaReaderFactory
	}
	if q.consumers <= 0 {
		q.consumers = 1
	}
	// Use manual commit control via CommitMessages in the handler path.
	q.readerCfg.CommitInterval = 0
	for i := 0; i < q.consumers; i++ {
		reader := q.readerFactory(q.readerCfg)
		q.logger.Info("starting kafka consumer",
			loggerpkg.F("index", i+1),
			loggerpkg.F("total", q.consumers),
			loggerpkg.F("topic", q.readerCfg.Topic),
			loggerpkg.F("group", q.readerCfg.GroupID),
		)
		go q.consume(ctx, reader, handler, i+1)
	}
	return nil
}

func (q *KafkaLogQueue) consume(ctx context.Context, reader kafkaReader, handler func(context.Context, model.ConsumedMessage), idx int) {
	defer func() {
		reader.Close()
		q.logger.Info("kafka consumer stopped", loggerpkg.F("index", idx))

	}()
	var rec model.LogRecord
	retryDelay := kafkaRetryMinBackoff
	retryRand := rand.New(rand.NewSource(time.Now().UTC().UnixNano() + int64(idx)))

	for {
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
				return
			}
			q.markKafkaDown(err)
			if !util.WaitForRetry(ctx, util.JitterRetryDelay(retryDelay, retryRand)) {
				return
			}
			retryDelay = util.NextRetryDelay(retryDelay, kafkaRetryMinBackoff, kafkaRetryMaxBackoff)
			continue
		}
		retryDelay = kafkaRetryMinBackoff
		q.markKafkaUp(msg.Partition, msg.Offset)
		active := atomic.AddInt32(&q.activeConsumers, 1)
		q.logger.Debug("kafka consumer processing message",
			loggerpkg.F("index", idx),
			loggerpkg.F("activeConsumers", active),
			loggerpkg.F("partition", msg.Partition),
			loggerpkg.F("offset", msg.Offset),
		)

		rec = model.LogRecord{} // reuse the same struct each iteration
		if err := json.Unmarshal(msg.Value, &rec); err != nil {
			q.logger.Warn("discard malformed kafka message", loggerpkg.F("error", err))
			atomic.AddInt32(&q.activeConsumers, -1)
			continue
		}
		commitFn := func(c context.Context) error {
			return reader.CommitMessages(c, msg)
		}
		handler(ctx, model.ConsumedMessage{
			Record:    rec,
			Partition: msg.Partition,
			Offset:    msg.Offset,
			Commit:    commitFn,
		})
		atomic.AddInt32(&q.activeConsumers, -1)
	}
}
