package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/lechuhuuha/log_forge/model"
	"github.com/lechuhuuha/log_forge/util"
)

var errObjectNotFound = errors.New("object not found")

type minioObjectStore interface {
	BucketExists(ctx context.Context, bucket string) (bool, error)
	ReadObject(ctx context.Context, bucket, object string) ([]byte, error)
	WriteObject(ctx context.Context, bucket, object string, payload []byte) error
}

type minioObjectStoreClient struct {
	client *minio.Client
}

func newMinIOObjectStoreClient(endpoint, accessKey, secretKey string, useSSL bool) (minioObjectStore, error) {
	minioClient, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("create minio client: %w", err)
	}
	return &minioObjectStoreClient{client: minioClient}, nil
}

func (c *minioObjectStoreClient) BucketExists(ctx context.Context, bucket string) (bool, error) {
	return c.client.BucketExists(ctx, bucket)
}

func (c *minioObjectStoreClient) ReadObject(ctx context.Context, bucket, object string) ([]byte, error) {
	obj, err := c.client.GetObject(ctx, bucket, object, minio.GetObjectOptions{})
	if err != nil {
		if isMinIONotFoundError(err) {
			return nil, errObjectNotFound
		}
		return nil, err
	}
	defer obj.Close()

	data, err := io.ReadAll(obj)
	if err != nil {
		if isMinIONotFoundError(err) {
			return nil, errObjectNotFound
		}
		return nil, err
	}
	return data, nil
}

func (c *minioObjectStoreClient) WriteObject(ctx context.Context, bucket, object string, payload []byte) error {
	_, err := c.client.PutObject(
		ctx,
		bucket,
		object,
		bytes.NewReader(payload),
		int64(len(payload)),
		minio.PutObjectOptions{ContentType: "application/x-ndjson"},
	)
	if err != nil {
		return err
	}
	return nil
}

func isMinIONotFoundError(err error) bool {
	if err == nil {
		return false
	}
	resp := minio.ToErrorResponse(err)
	if resp.Code == "NoSuchKey" || resp.Code == "NoSuchObject" {
		return true
	}
	return resp.StatusCode == http.StatusNotFound
}

// MinIORepoOptions configures a MinIO-backed repository.
type MinIORepoOptions struct {
	Endpoint   string
	Bucket     string
	AccessKey  string
	SecretKey  string
	UseSSL     bool
	LogsPrefix string

	client minioObjectStore
}

// MinIORepo persists logs into MinIO-compatible object storage.
type MinIORepo struct {
	client     minioObjectStore
	bucket     string
	logsPrefix string

	mu          sync.Mutex
	objectLocks map[string]*sync.Mutex
}

// NewMinIORepo creates a MinIO-backed Repository implementation.
func NewMinIORepo(opts MinIORepoOptions) (*MinIORepo, error) {
	bucket := strings.TrimSpace(opts.Bucket)
	if bucket == "" {
		return nil, errors.New("minio bucket is required")
	}
	logsPrefix := strings.Trim(strings.TrimSpace(opts.LogsPrefix), "/")
	if logsPrefix == "" {
		logsPrefix = "logs"
	}

	storeClient := opts.client
	if storeClient == nil {
		endpoint := strings.TrimSpace(opts.Endpoint)
		accessKey := strings.TrimSpace(opts.AccessKey)
		secretKey := strings.TrimSpace(opts.SecretKey)
		if endpoint == "" {
			return nil, errors.New("minio endpoint is required")
		}
		if accessKey == "" {
			return nil, errors.New("minio access key is required")
		}
		if secretKey == "" {
			return nil, errors.New("minio secret key is required")
		}
		client, err := newMinIOObjectStoreClient(endpoint, accessKey, secretKey, opts.UseSSL)
		if err != nil {
			return nil, err
		}
		storeClient = client
	}

	return &MinIORepo{
		client:      storeClient,
		bucket:      bucket,
		logsPrefix:  logsPrefix,
		objectLocks: make(map[string]*sync.Mutex),
	}, nil
}

// CheckReady validates that the configured bucket is reachable.
func (r *MinIORepo) CheckReady(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	exists, err := r.client.BucketExists(ctx, r.bucket)
	if err != nil {
		return fmt.Errorf("check minio bucket: %w", err)
	}
	if !exists {
		return fmt.Errorf("minio bucket %q does not exist", r.bucket)
	}
	return nil
}

// SaveBatch appends the provided records to hourly NDJSON objects.
func (r *MinIORepo) SaveBatch(ctx context.Context, records []model.LogRecord) error {
	if len(records) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	grouped := make(map[string][]model.LogRecord)
	for _, rec := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		ts := rec.Timestamp.UTC()
		rec.Timestamp = ts
		object := path.Join(
			r.logsPrefix,
			ts.Format(util.DateLayout),
			fmt.Sprintf("%s.log.json", ts.Format(util.HourLayout)),
		)
		grouped[object] = append(grouped[object], rec)
	}

	for object, group := range grouped {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.appendRecords(ctx, object, group); err != nil {
			return err
		}
	}
	return nil
}

func (r *MinIORepo) appendRecords(ctx context.Context, object string, records []model.LogRecord) error {
	lock := r.lockFor(object)
	lock.Lock()
	defer lock.Unlock()

	existing, err := r.client.ReadObject(ctx, r.bucket, object)
	if err != nil && !errors.Is(err, errObjectNotFound) {
		return fmt.Errorf("read object %q: %w", object, err)
	}

	var payload bytes.Buffer
	if !errors.Is(err, errObjectNotFound) && len(existing) > 0 {
		payload.Write(existing)
	}

	for _, rec := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("marshal log record: %w", err)
		}
		payload.Write(data)
		payload.WriteByte('\n')
	}

	if err := r.client.WriteObject(ctx, r.bucket, object, payload.Bytes()); err != nil {
		return fmt.Errorf("write object %q: %w", object, err)
	}
	return nil
}

func (r *MinIORepo) lockFor(object string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	lock, ok := r.objectLocks[object]
	if !ok {
		lock = &sync.Mutex{}
		r.objectLocks[object] = lock
	}
	return lock
}
