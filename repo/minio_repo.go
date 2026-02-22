package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/lechuhuuha/log_forge/model"
	"github.com/lechuhuuha/log_forge/util"
)

var errObjectNotFound = errors.New("object not found")

// MinIOObjectInfo provides object metadata used by aggregation reads.
type MinIOObjectInfo struct {
	Key          string
	LastModified time.Time
}

type minioObjectStore interface {
	BucketExists(ctx context.Context, bucket string) (bool, error)
	ReadObject(ctx context.Context, bucket, object string) ([]byte, error)
	WriteObject(ctx context.Context, bucket, object string, payload []byte) error
	ListObjects(ctx context.Context, bucket, prefix string) ([]MinIOObjectInfo, error)
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

func (c *minioObjectStoreClient) ListObjects(ctx context.Context, bucket, prefix string) ([]MinIOObjectInfo, error) {
	objects := make([]MinIOObjectInfo, 0)
	for obj := range c.client.ListObjects(ctx, bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		objects = append(objects, MinIOObjectInfo{Key: obj.Key, LastModified: obj.LastModified})
	}
	return objects, nil
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
	// WriterID identifies this writer for sharded object keys.
	WriterID string

	client minioObjectStore
}

// MinIORepo persists logs into MinIO-compatible object storage.
type MinIORepo struct {
	client     minioObjectStore
	bucket     string
	logsPrefix string
	writerID   string
	objectSeq  atomic.Uint64
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

	writerBase := sanitizeWriterID(opts.WriterID)
	if writerBase == "" {
		writerBase = defaultWriterID()
	}
	writerID := fmt.Sprintf("%s-%d-%d", writerBase, os.Getpid(), time.Now().UTC().UnixNano())

	return &MinIORepo{
		client:     storeClient,
		bucket:     bucket,
		logsPrefix: logsPrefix,
		writerID:   writerID,
	}, nil
}

func sanitizeWriterID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func defaultWriterID() string {
	for _, candidate := range []string{
		os.Getenv("LOGFORGE_WRITER_ID"),
		os.Getenv("POD_NAME"),
		os.Getenv("HOSTNAME"),
	} {
		id := sanitizeWriterID(candidate)
		if id != "" {
			return id
		}
	}
	if host, err := os.Hostname(); err == nil {
		if id := sanitizeWriterID(host); id != "" {
			return id
		}
	}
	return "writer"
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

// SaveBatch stores records into sharded hourly NDJSON objects.
//
// Keys follow: logs/<YYYY-MM-DD>/<HH>/<writer-seq>.log.json.
// This avoids cross-replica lost updates caused by read-modify-write append.
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
		hourPrefix := path.Join(r.logsPrefix, ts.Format(util.DateLayout), ts.Format(util.HourLayout))
		grouped[hourPrefix] = append(grouped[hourPrefix], rec)
	}

	prefixes := make([]string, 0, len(grouped))
	for prefix := range grouped {
		prefixes = append(prefixes, prefix)
	}
	sort.Strings(prefixes)

	for _, hourPrefix := range prefixes {
		if err := ctx.Err(); err != nil {
			return err
		}
		payload, err := marshalNDJSON(ctx, grouped[hourPrefix])
		if err != nil {
			return err
		}
		object := r.newShardObjectKey(hourPrefix)
		if err := r.client.WriteObject(ctx, r.bucket, object, payload); err != nil {
			return fmt.Errorf("write object %q: %w", object, err)
		}
	}
	return nil
}

func marshalNDJSON(ctx context.Context, records []model.LogRecord) ([]byte, error) {
	var payload bytes.Buffer
	for _, rec := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := json.Marshal(rec)
		if err != nil {
			return nil, fmt.Errorf("marshal log record: %w", err)
		}
		payload.Write(data)
		payload.WriteByte('\n')
	}
	return payload.Bytes(), nil
}

func (r *MinIORepo) newShardObjectKey(hourPrefix string) string {
	seq := r.objectSeq.Add(1)
	file := r.writerID + "-" + strconv.FormatUint(seq, 10) + ".log.json"
	return path.Join(hourPrefix, file)
}

// ListLogObjects lists log objects under the logs prefix.
func (r *MinIORepo) ListLogObjects(ctx context.Context) ([]MinIOObjectInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	prefix := strings.Trim(r.logsPrefix, "/") + "/"
	objects, err := r.client.ListObjects(ctx, r.bucket, prefix)
	if err != nil {
		return nil, fmt.Errorf("list objects with prefix %q: %w", prefix, err)
	}
	filtered := make([]MinIOObjectInfo, 0, len(objects))
	for _, object := range objects {
		if strings.HasSuffix(object.Key, ".log.json") {
			filtered = append(filtered, object)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Key < filtered[j].Key
	})
	return filtered, nil
}

// ReadLogObject reads a single log object payload.
func (r *MinIORepo) ReadLogObject(ctx context.Context, object string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	object = strings.TrimPrefix(strings.TrimSpace(object), "/")
	if object == "" {
		return nil, errors.New("object key is required")
	}
	data, err := r.client.ReadObject(ctx, r.bucket, object)
	if err != nil {
		return nil, fmt.Errorf("read object %q: %w", object, err)
	}
	return data, nil
}
