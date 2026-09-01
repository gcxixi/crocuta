package sentryx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const DefaultMaxBlobBytes int64 = 25 << 20

// BlobStore holds large or sensitive artifacts outside of the relational
// metadata tables. Implementations must treat key as an opaque, relative key.
// Source-map metadata and lookup indexes remain in PostgreSQL.
type BlobStore interface {
	Put(ctx context.Context, key string, body []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
}

// NewBlobStoreFromEnv selects the configured artifact backend. The default is
// database-only for backwards compatibility; file and S3 modes are explicit.
func NewBlobStoreFromEnv() (BlobStore, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("SENTRYX_BLOB_BACKEND")))
	switch backend {
	case "", "database", "postgres", "none":
		return nil, nil
	case "file":
		return NewFileBlobStore(os.Getenv("SENTRYX_BLOB_DIR"))
	case "s3":
		secure := true
		if value := os.Getenv("SENTRYX_S3_SECURE"); value != "" {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return nil, fmt.Errorf("invalid SENTRYX_S3_SECURE: %w", err)
			}
			secure = parsed
		}
		endpoint := strings.TrimSpace(os.Getenv("SENTRYX_S3_ENDPOINT"))
		if strings.HasPrefix(endpoint, "https://") {
			endpoint = strings.TrimPrefix(endpoint, "https://")
		} else if strings.HasPrefix(endpoint, "http://") {
			endpoint = strings.TrimPrefix(endpoint, "http://")
			secure = false
		}
		return NewS3BlobStore(endpoint, os.Getenv("SENTRYX_S3_ACCESS_KEY"), os.Getenv("SENTRYX_S3_SECRET_KEY"), os.Getenv("SENTRYX_S3_REGION"), os.Getenv("SENTRYX_S3_BUCKET"), os.Getenv("SENTRYX_S3_PREFIX"), secure)
	default:
		return nil, fmt.Errorf("unsupported SENTRYX_BLOB_BACKEND %q", backend)
	}
}

// FileBlobStore is intended for local development and single-node smoke tests.
// Keys are normalized below Root and cannot escape it.
type FileBlobStore struct {
	Root    string
	MaxSize int64
}

func NewFileBlobStore(root string) (*FileBlobStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("blob directory is required")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, err
	}
	return &FileBlobStore{Root: root, MaxSize: DefaultMaxBlobBytes}, nil
}

func (f *FileBlobStore) Put(ctx context.Context, key string, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.MaxSize > 0 && int64(len(body)) > f.MaxSize {
		return fmt.Errorf("blob exceeds %d bytes", f.MaxSize)
	}
	filename, err := f.filename(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(path.Dir(filename), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(path.Dir(filename), ".sentryx-blob-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o640); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, filename); err != nil {
		return err
	}
	return nil
}

func (f *FileBlobStore) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	filename, err := f.filename(key)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	limit := f.MaxSize
	if limit <= 0 {
		limit = DefaultMaxBlobBytes
	}
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("blob exceeds %d bytes", limit)
	}
	return body, nil
}

func (f *FileBlobStore) filename(key string) (string, error) {
	clean := strings.TrimPrefix(path.Clean("/"+key), "/")
	if clean == "" || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("invalid blob key")
	}
	return path.Join(f.Root, clean), nil
}

// S3BlobStore works with AWS S3 and S3-compatible services such as MinIO.
// The MinIO client performs SigV4 signing and keeps credentials out of event
// payloads and logs.
type S3BlobStore struct {
	Client  *minio.Client
	Bucket  string
	Prefix  string
	MaxSize int64
}

func NewS3BlobStore(endpoint, accessKey, secretKey, region, bucket, prefix string, secure bool) (*S3BlobStore, error) {
	if endpoint == "" || accessKey == "" || secretKey == "" || bucket == "" {
		return nil, errors.New("s3 endpoint, credentials, and bucket are required")
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: secure,
		Region: region,
	})
	if err != nil {
		return nil, err
	}
	return &S3BlobStore{Client: client, Bucket: bucket, Prefix: strings.Trim(prefix, "/"), MaxSize: DefaultMaxBlobBytes}, nil
}

func (s *S3BlobStore) Put(ctx context.Context, key string, body []byte) error {
	if s.MaxSize > 0 && int64(len(body)) > s.MaxSize {
		return fmt.Errorf("blob exceeds %d bytes", s.MaxSize)
	}
	_, err := s.Client.PutObject(ctx, s.Bucket, s.objectKey(key), bytes.NewReader(body), int64(len(body)), minio.PutObjectOptions{ContentType: "application/json"})
	return err
}

func (s *S3BlobStore) Get(ctx context.Context, key string) ([]byte, error) {
	object, err := s.Client.GetObject(ctx, s.Bucket, s.objectKey(key), minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer object.Close()
	limit := s.MaxSize
	if limit <= 0 {
		limit = DefaultMaxBlobBytes
	}
	body, err := io.ReadAll(io.LimitReader(object, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("blob exceeds %d bytes", limit)
	}
	return body, nil
}

func (s *S3BlobStore) objectKey(key string) string {
	clean := strings.TrimPrefix(path.Clean("/"+key), "/")
	if s.Prefix == "" {
		return clean
	}
	return path.Join(s.Prefix, clean)
}
