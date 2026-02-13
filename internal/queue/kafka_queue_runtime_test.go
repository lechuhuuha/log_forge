package queue

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kafka "github.com/segmentio/kafka-go"

	loggerpkg "github.com/lechuhuuha/log_forge/logger"
	"github.com/lechuhuuha/log_forge/model"
)

type fakeKafkaWriter struct {
	mu         sync.Mutex
	writes     [][]kafka.Message
	writeErr   error
	closeErr   error
	closeCalls int
}

func (w *fakeKafkaWriter) WriteMessages(_ context.Context, msgs ...kafka.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	cp := make([]kafka.Message, len(msgs))
	copy(cp, msgs)
	w.writes = append(w.writes, cp)
	return w.writeErr
}

func (w *fakeKafkaWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closeCalls++
	return w.closeErr
}

type fetchStep struct {
	msg kafka.Message
	err error
}

type fakeKafkaReader struct {
	mu          sync.Mutex
	steps       []fetchStep
	idx         int
	commitCalls int
	commitErr   error
	closed      chan struct{}
	closeOnce   sync.Once
}

func newFakeKafkaReader(steps []fetchStep) *fakeKafkaReader {
	return &fakeKafkaReader{
		steps:  steps,
		closed: make(chan struct{}),
	}
}

func (r *fakeKafkaReader) FetchMessage(_ context.Context) (kafka.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.idx >= len(r.steps) {
		return kafka.Message{}, context.Canceled
	}
	step := r.steps[r.idx]
	r.idx++
	return step.msg, step.err
}

func (r *fakeKafkaReader) CommitMessages(_ context.Context, _ ...kafka.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commitCalls++
	return r.commitErr
}

func (r *fakeKafkaReader) Close() error {
	r.closeOnce.Do(func() {
		close(r.closed)
	})
	return nil
}

func TestKafkaLogQueue_EnqueueBatch(t *testing.T) {
	cases := []struct {
		name       string
		records    []model.LogRecord
		writerErr  error
		wantErr    bool
		wantWrites int
	}{
		{
			name:       "empty records is no-op",
			records:    nil,
			wantErr:    false,
			wantWrites: 0,
		},
		{
			name: "writer error is returned",
			records: []model.LogRecord{{
				Timestamp: time.Now().UTC(),
				Path:      "/err",
				UserAgent: "ua",
			}},
			writerErr:  errors.New("write failed"),
			wantErr:    true,
			wantWrites: 1,
		},
		{
			name: "writes encoded messages",
			records: []model.LogRecord{{
				Timestamp: time.Now().UTC(),
				Path:      "/ok",
				UserAgent: "ua",
			}},
			wantErr:    false,
			wantWrites: 1,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			writer := &fakeKafkaWriter{writeErr: tc.writerErr}
			q := &KafkaLogQueue{
				writer: writer,
				logger: loggerpkg.NewNop(),
			}

			err := q.EnqueueBatch(context.Background(), tc.records)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			writer.mu.Lock()
			defer writer.mu.Unlock()
			if len(writer.writes) != tc.wantWrites {
				t.Fatalf("unexpected writes count: got=%d want=%d", len(writer.writes), tc.wantWrites)
			}
			if len(tc.records) > 0 && len(writer.writes) > 0 {
				var got model.LogRecord
				if err := json.Unmarshal(writer.writes[0][0].Value, &got); err != nil {
					t.Fatalf("failed to decode written message: %v", err)
				}
				if got.Path != tc.records[0].Path {
					t.Fatalf("unexpected written path: got=%q want=%q", got.Path, tc.records[0].Path)
				}
			}
		})
	}
}

func TestKafkaLogQueue_ConsumerFlow(t *testing.T) {
	validRecord := model.LogRecord{
		Timestamp: time.Now().UTC(),
		Path:      "/home",
		UserAgent: "ua",
	}
	validData, err := json.Marshal(validRecord)
	if err != nil {
		t.Fatalf("marshal test record: %v", err)
	}

	cases := []struct {
		name          string
		steps         []fetchStep
		run           func(t *testing.T, q *KafkaLogQueue, reader *fakeKafkaReader)
		wantDownState bool
	}{
		{
			name: "valid message triggers handler and commit",
			steps: []fetchStep{
				{msg: kafka.Message{Value: validData, Partition: 1, Offset: 42}},
				{err: context.Canceled},
			},
			run: func(t *testing.T, q *KafkaLogQueue, reader *fakeKafkaReader) {
				handled := make(chan model.ConsumedMessage, 1)
				if err := q.StartConsumers(context.Background(), func(_ context.Context, msg model.ConsumedMessage) {
					if msg.Commit != nil {
						if err := msg.Commit(context.Background()); err != nil {
							t.Fatalf("commit failed: %v", err)
						}
					}
					handled <- msg
				}); err != nil {
					t.Fatalf("StartConsumers returned error: %v", err)
				}

				select {
				case msg := <-handled:
					if msg.Record.Path != "/home" {
						t.Fatalf("unexpected record path: got=%q", msg.Record.Path)
					}
				case <-time.After(time.Second):
					t.Fatal("timed out waiting for handler")
				}
				waitReaderClosed(t, reader)
				reader.mu.Lock()
				defer reader.mu.Unlock()
				if reader.commitCalls != 1 {
					t.Fatalf("expected 1 commit, got %d", reader.commitCalls)
				}
			},
			wantDownState: false,
		},
		{
			name: "malformed message is discarded",
			steps: []fetchStep{
				{msg: kafka.Message{Value: []byte("{not-json"), Partition: 1, Offset: 1}},
				{err: context.Canceled},
			},
			run: func(t *testing.T, q *KafkaLogQueue, reader *fakeKafkaReader) {
				called := make(chan struct{}, 1)
				if err := q.StartConsumers(context.Background(), func(_ context.Context, _ model.ConsumedMessage) {
					called <- struct{}{}
				}); err != nil {
					t.Fatalf("StartConsumers returned error: %v", err)
				}
				waitReaderClosed(t, reader)
				select {
				case <-called:
					t.Fatal("handler should not be called for malformed message")
				default:
				}
				if got := atomic.LoadInt32(&q.activeConsumers); got != 0 {
					t.Fatalf("expected activeConsumers=0, got %d", got)
				}
			},
			wantDownState: false,
		},
		{
			name: "fetch error marks kafka down then exits on canceled context",
			steps: []fetchStep{
				{err: errors.New("fetch failed")},
			},
			run: func(t *testing.T, q *KafkaLogQueue, reader *fakeKafkaReader) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if err := q.StartConsumers(ctx, func(_ context.Context, _ model.ConsumedMessage) {}); err != nil {
					t.Fatalf("StartConsumers returned error: %v", err)
				}
				waitFor(t, time.Second, func() bool {
					return q.kafkaDown.Load()
				})
				cancel()
				waitReaderClosed(t, reader)
			},
			wantDownState: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			reader := newFakeKafkaReader(tc.steps)
			q := &KafkaLogQueue{
				readerCfg: kafka.ReaderConfig{
					Topic:   "logs",
					GroupID: "g1",
				},
				readerFactory: func(kafka.ReaderConfig) kafkaReader { return reader },
				consumers:     1,
				logger:        loggerpkg.NewNop(),
			}

			tc.run(t, q, reader)
			if q.kafkaDown.Load() != tc.wantDownState {
				t.Fatalf("unexpected kafkaDown state: got=%v want=%v", q.kafkaDown.Load(), tc.wantDownState)
			}
		})
	}
}

func TestKafkaLogQueue_CheckConnectivitySuccess(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	q := &KafkaLogQueue{
		readerCfg: kafka.ReaderConfig{
			Brokers: []string{"127.0.0.1:1", listener.Addr().String()},
		},
		logger: loggerpkg.NewNop(),
	}
	q.kafkaDown.Store(true)

	if err := q.CheckConnectivity(nil); err != nil {
		t.Fatalf("CheckConnectivity returned error: %v", err)
	}
	if q.kafkaDown.Load() {
		t.Fatal("expected kafkaDown to be false after successful connectivity check")
	}

	_ = listener.Close()
	<-done
}

func TestKafkaLogQueue_StateHelpers(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, q *KafkaLogQueue)
	}{
		{
			name: "markKafkaDown sets initial timestamp",
			run: func(t *testing.T, q *KafkaLogQueue) {
				q.markKafkaDown(errors.New("down"))
				if !q.kafkaDown.Load() {
					t.Fatal("expected kafkaDown=true")
				}
				if q.lastDownLogNs.Load() == 0 {
					t.Fatal("expected lastDownLogNs to be set")
				}
			},
		},
		{
			name: "markKafkaDown throttles repeated logs",
			run: func(t *testing.T, q *KafkaLogQueue) {
				q.kafkaDown.Store(true)
				now := time.Now().UTC().UnixNano()
				q.lastDownLogNs.Store(now)
				q.markKafkaDown(errors.New("still down"))
				if got := q.lastDownLogNs.Load(); got != now {
					t.Fatalf("expected lastDownLogNs unchanged, got=%d want=%d", got, now)
				}
			},
		},
		{
			name: "markKafkaDown logs after interval passes",
			run: func(t *testing.T, q *KafkaLogQueue) {
				q.kafkaDown.Store(true)
				past := time.Now().UTC().Add(-2 * kafkaDownLogInterval).UnixNano()
				q.lastDownLogNs.Store(past)
				q.markKafkaDown(errors.New("still down"))
				if got := q.lastDownLogNs.Load(); got <= past {
					t.Fatalf("expected lastDownLogNs to advance, got=%d past=%d", got, past)
				}
			},
		},
		{
			name: "markKafkaUp clears down flag",
			run: func(t *testing.T, q *KafkaLogQueue) {
				q.kafkaDown.Store(true)
				q.markKafkaUp(1, 2)
				if q.kafkaDown.Load() {
					t.Fatal("expected kafkaDown=false")
				}
			},
		},
		{
			name: "logWriterError throttles repeated logs",
			run: func(t *testing.T, q *KafkaLogQueue) {
				q.logWriterError("sample")
				first := q.lastWriterLogNs.Load()
				if first == 0 {
					t.Fatal("expected first writer log timestamp")
				}
				q.logWriterError("sample")
				if got := q.lastWriterLogNs.Load(); got != first {
					t.Fatalf("expected writer log timestamp unchanged, got=%d want=%d", got, first)
				}
			},
		},
		{
			name: "logWriterError updates after interval",
			run: func(t *testing.T, q *KafkaLogQueue) {
				past := time.Now().UTC().Add(-2 * kafkaWriterLogInterval).UnixNano()
				q.lastWriterLogNs.Store(past)
				q.logWriterError("sample")
				if got := q.lastWriterLogNs.Load(); got <= past {
					t.Fatalf("expected writer log timestamp to advance, got=%d past=%d", got, past)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			q := &KafkaLogQueue{logger: loggerpkg.NewNop()}
			tc.run(t, q)
		})
	}
}

func waitReaderClosed(t *testing.T, reader *fakeKafkaReader) {
	t.Helper()
	select {
	case <-reader.closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reader to close")
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}
