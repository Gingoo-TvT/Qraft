package minio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"sync"
	"time"

	miniogo "github.com/minio/minio-go/v7"
)

const contentDigestCacheEntries = 4096

// Object metadata is a change detector, never the content digest itself.
// SHA-256 is always computed from the actual stream on a cache miss.
type contentObjectVersion struct {
	VersionID string
	ETag      string
	Size      int64
	Modified  time.Time
}

func (v contentObjectVersion) same(other contentObjectVersion) bool {
	return v.VersionID == other.VersionID && v.ETag == other.ETag && v.Size == other.Size && v.Modified.Equal(other.Modified)
}
func (v contentObjectVersion) valid() bool {
	return v.Size >= 0 && v.Size < math.MaxInt64 && (v.ETag != "" || v.VersionID != "" && v.VersionID != "null")
}

type contentDigestSource interface {
	stat(context.Context, string) (contentObjectVersion, error)
	open(context.Context, string, contentObjectVersion) (io.ReadCloser, error)
}
type minioDigestSource struct{ client *MinIOClient }

func (s minioDigestSource) stat(ctx context.Context, key string) (contentObjectVersion, error) {
	if s.client == nil || s.client.client == nil {
		return contentObjectVersion{}, fmt.Errorf("object storage is not configured")
	}
	info, err := s.client.client.StatObject(ctx, s.client.bucket, key, miniogo.StatObjectOptions{})
	if err != nil {
		return contentObjectVersion{}, err
	}
	return contentObjectVersion{VersionID: info.VersionID, ETag: info.ETag, Size: info.Size, Modified: info.LastModified}, nil
}
func (s minioDigestSource) open(ctx context.Context, key string, version contentObjectVersion) (io.ReadCloser, error) {
	opts := miniogo.GetObjectOptions{VersionID: version.VersionID}
	if version.ETag != "" {
		if err := opts.SetMatchETag(version.ETag); err != nil {
			return nil, err
		}
	}
	return s.client.client.GetObject(ctx, s.client.bucket, key, opts)
}

type contentDigestEntry struct {
	version contentObjectVersion
	digest  string
}
type contentDigester struct {
	source   contentDigestSource
	mu       sync.Mutex
	entries  map[string]contentDigestEntry
	order    []string
	next     int
	capacity int
}

func newContentDigester(source contentDigestSource, capacity int) *contentDigester {
	if capacity < 1 {
		capacity = 1
	}
	return &contentDigester{source: source, entries: make(map[string]contentDigestEntry), capacity: capacity}
}

// NewContentDigester returns a process-local bounded digest cache. Each call
// performs HEAD, even on a hit. Misses stream bytes and require identical
// metadata before/after reading. It starts no goroutines and stores no payloads.
func NewContentDigester(client *MinIOClient) func(context.Context, string) (string, error) {
	return newContentDigester(minioDigestSource{client: client}, contentDigestCacheEntries).digest
}
func (d *contentDigester) digest(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	before, err := d.source.stat(ctx, key)
	if err != nil {
		return "", fmt.Errorf("stat test content: %w", err)
	}
	if !before.valid() {
		return "", fmt.Errorf("test object lacks stable version metadata")
	}
	d.mu.Lock()
	cached, ok := d.entries[key]
	d.mu.Unlock()
	if ok && before.same(cached.version) {
		return cached.digest, nil
	}
	body, err := d.source.open(ctx, key, before)
	if err != nil {
		return "", fmt.Errorf("open test content: %w", err)
	}
	h := sha256.New()
	// The extra byte detects a stream larger than its HEAD without buffering it.
	count, readErr := io.Copy(h, io.LimitReader(contentContextReader{ctx: ctx, reader: body}, before.Size+1))
	closeErr := body.Close()
	if readErr != nil {
		return "", fmt.Errorf("hash test content: %w", readErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close test content: %w", closeErr)
	}
	if count != before.Size {
		return "", fmt.Errorf("test content size changed while hashing")
	}
	after, err := d.source.stat(ctx, key)
	if err != nil {
		return "", fmt.Errorf("recheck test content: %w", err)
	}
	if !before.same(after) {
		return "", fmt.Errorf("test object changed while hashing; retry on a stable version")
	}
	digest := hex.EncodeToString(h.Sum(nil))
	d.mu.Lock()
	// Concurrent misses may duplicate a read, but never share mutable hash state.
	if _, exists := d.entries[key]; !exists {
		if len(d.order) < d.capacity {
			d.order = append(d.order, key)
		} else {
			delete(d.entries, d.order[d.next])
			d.order[d.next] = key
			d.next = (d.next + 1) % d.capacity
		}
	}
	d.entries[key] = contentDigestEntry{version: after, digest: digest}
	d.mu.Unlock()
	return digest, nil
}

type contentContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contentContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// DownloadFileLimited is for inline samples. The limit is enforced while
// reading, so an incorrect oversized sample cannot consume unbounded memory.
func (m *MinIOClient) DownloadFileLimited(ctx context.Context, path string, maxBytes int64) ([]byte, error) {
	if maxBytes < 0 || maxBytes == math.MaxInt64 {
		return nil, fmt.Errorf("invalid sample size limit")
	}
	if m == nil || m.client == nil {
		return nil, fmt.Errorf("object storage is not configured")
	}
	object, err := m.client.GetObject(ctx, m.bucket, path, miniogo.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get sample content: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(contentContextReader{ctx: ctx, reader: object}, maxBytes+1))
	closeErr := object.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read sample content: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close sample content: %w", closeErr)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("sample content exceeds %d bytes", maxBytes)
	}
	return data, nil
}
