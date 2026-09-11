// Package minio provides a MinIO / S3-compatible object storage client
// tailored for AlgoForge's test data management. It handles uploading,
// downloading, and organising test input/output files by problem ID.
package minio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	miniogo "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/rs/zerolog"

	"github.com/Gingoo-TvT/Qraft/backend/internal/config"
)

const (
	// testdataPrefix is the key prefix under which all problem test data is stored.
	testdataPrefix = "testdata"

	// defaultContentType is used when uploading test data files.
	defaultContentType = "application/octet-stream"
)

// ---------------------------------------------------------------------------
// MinIOClient
// ---------------------------------------------------------------------------

// MinIOClient wraps the minio-go client with convenience methods for
// AlgoForge's object storage needs.
type MinIOClient struct {
	client     *miniogo.Client
	bucket     string
	logger     zerolog.Logger
}

// NewMinIOClient creates a new MinIOClient from the application's MinIO
// configuration. It initialises the underlying minio-go client and verifies
// connectivity by checking (and optionally creating) the configured bucket.
func NewMinIOClient(cfg config.MinIOConfig, logger zerolog.Logger) (*MinIOClient, error) {
	client, err := miniogo.New(cfg.Endpoint, &miniogo.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("creating minio client: %w", err)
	}

	mc := &MinIOClient{
		client: client,
		bucket: cfg.Bucket,
		logger: logger.With().Str("component", "minio").Logger(),
	}

	mc.logger.Info().
		Str("endpoint", cfg.Endpoint).
		Str("bucket", cfg.Bucket).
		Bool("ssl", cfg.UseSSL).
		Msg("minio client initialised")

	return mc, nil
}

// ---------------------------------------------------------------------------
// Bucket management
// ---------------------------------------------------------------------------

// EnsureBucket creates the bucket if it does not already exist.
func (m *MinIOClient) EnsureBucket(ctx context.Context, bucketName string) error {
	exists, err := m.client.BucketExists(ctx, bucketName)
	if err != nil {
		return fmt.Errorf("checking bucket existence: %w", err)
	}
	if exists {
		m.logger.Debug().Str("bucket", bucketName).Msg("bucket already exists")
		return nil
	}

	if err := m.client.MakeBucket(ctx, bucketName, miniogo.MakeBucketOptions{}); err != nil {
		return fmt.Errorf("creating bucket %q: %w", bucketName, err)
	}

	m.logger.Info().Str("bucket", bucketName).Msg("bucket created")
	return nil
}

// EnsureDefaultBucket creates the default bucket configured for this client
// if it does not already exist.
func (m *MinIOClient) EnsureDefaultBucket(ctx context.Context) error {
	return m.EnsureBucket(ctx, m.bucket)
}

// ---------------------------------------------------------------------------
// Test data operations
// ---------------------------------------------------------------------------

// testInputPath returns the object key for a test case's input file.
func testInputPath(problemID uuid.UUID, testIndex int) string {
	return fmt.Sprintf("%s/%s/%d.in", testdataPrefix, problemID.String(), testIndex)
}

// testOutputPath returns the object key for a test case's expected output file.
func testOutputPath(problemID uuid.UUID, testIndex int) string {
	return fmt.Sprintf("%s/%s/%d.out", testdataPrefix, problemID.String(), testIndex)
}

// UploadTestData uploads the input and expected output for a single test case.
// It returns the object paths used for storage, which can be recorded in the
// database alongside the test case record.
func (m *MinIOClient) UploadTestData(ctx context.Context, problemID uuid.UUID, testIndex int, input []byte, output []byte) (inputPath, outputPath string, err error) {
	inputPath = testInputPath(problemID, testIndex)
	outputPath = testOutputPath(problemID, testIndex)

	if err := m.uploadBytes(ctx, inputPath, input); err != nil {
		return "", "", fmt.Errorf("uploading test input: %w", err)
	}

	if err := m.uploadBytes(ctx, outputPath, output); err != nil {
		return "", "", fmt.Errorf("uploading test output: %w", err)
	}

	m.logger.Debug().
		Str("problem_id", problemID.String()).
		Int("test_index", testIndex).
		Msg("test data uploaded")

	return inputPath, outputPath, nil
}

// DownloadFile retrieves the contents of an object at the given path.
func (m *MinIOClient) DownloadFile(ctx context.Context, path string) ([]byte, error) {
	obj, err := m.client.GetObject(ctx, m.bucket, path, miniogo.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting object %q: %w", path, err)
	}
	defer obj.Close()

	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("reading object %q: %w", path, err)
	}

	return data, nil
}

// DeleteProblemData removes all objects associated with the given problem ID
// from the test data prefix.
func (m *MinIOClient) DeleteProblemData(ctx context.Context, problemID uuid.UUID) error {
	prefix := fmt.Sprintf("%s/%s/", testdataPrefix, problemID.String())

	objectsCh := m.client.ListObjects(ctx, m.bucket, miniogo.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})

	var errs []string
	for obj := range objectsCh {
		if obj.Err != nil {
			errs = append(errs, obj.Err.Error())
			continue
		}

		if err := m.client.RemoveObject(ctx, m.bucket, obj.Key, miniogo.RemoveObjectOptions{}); err != nil {
			errs = append(errs, fmt.Sprintf("removing %q: %v", obj.Key, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors deleting problem data: %s", strings.Join(errs, "; "))
	}

	m.logger.Info().
		Str("problem_id", problemID.String()).
		Str("prefix", prefix).
		Msg("problem data deleted")

	return nil
}

// ListProblemFiles returns all object keys stored under the test data prefix
// for the given problem ID.
func (m *MinIOClient) ListProblemFiles(ctx context.Context, problemID uuid.UUID) ([]string, error) {
	prefix := fmt.Sprintf("%s/%s/", testdataPrefix, problemID.String())

	objectsCh := m.client.ListObjects(ctx, m.bucket, miniogo.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})

	var paths []string
	for obj := range objectsCh {
		if obj.Err != nil {
			return nil, fmt.Errorf("listing objects with prefix %q: %w", prefix, obj.Err)
		}
		paths = append(paths, obj.Key)
	}

	return paths, nil
}

// ---------------------------------------------------------------------------
// Generic helpers
// ---------------------------------------------------------------------------

// UploadBytes uploads arbitrary bytes to the given object key.
func (m *MinIOClient) UploadBytes(ctx context.Context, key string, data []byte) error {
	return m.uploadBytes(ctx, key, data)
}

// uploadBytes is the internal upload implementation.
func (m *MinIOClient) uploadBytes(ctx context.Context, key string, data []byte) error {
	reader := bytes.NewReader(data)
	_, err := m.client.PutObject(ctx, m.bucket, key, reader, int64(len(data)), miniogo.PutObjectOptions{
		ContentType: defaultContentType,
	})
	if err != nil {
		return fmt.Errorf("putting object %q: %w", key, err)
	}
	return nil
}

// Bucket returns the configured bucket name.
func (m *MinIOClient) Bucket() string {
	return m.bucket
}

// StatFile returns the size (in bytes) of the object at the given path.
func (m *MinIOClient) StatFile(ctx context.Context, path string) (int64, error) {
	info, err := m.client.StatObject(ctx, m.bucket, path, miniogo.StatObjectOptions{})
	if err != nil {
		return 0, fmt.Errorf("stat object %q: %w", path, err)
	}
	return info.Size, nil
}

// Client returns the underlying minio-go client for advanced operations.
func (m *MinIOClient) Client() *miniogo.Client {
	return m.client
}
