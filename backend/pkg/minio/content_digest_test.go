package minio

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	miniogo "github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/require"
)

type digestFixtureObject struct {
	version contentObjectVersion
	data    []byte
}
type digestFixtureSource struct {
	mu            sync.Mutex
	objects       map[string]digestFixtureObject
	stats         int
	opens         int
	openedVersion contentObjectVersion
	afterOpen     func(*digestFixtureSource, string)
}

func (s *digestFixtureSource) stat(_ context.Context, key string) (contentObjectVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats++
	object, ok := s.objects[key]
	if !ok {
		return contentObjectVersion{}, fmt.Errorf("missing fixture")
	}
	return object.version, nil
}
func (s *digestFixtureSource) open(_ context.Context, key string, version contentObjectVersion) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opens++
	s.openedVersion = version
	object := s.objects[key]
	data := append([]byte(nil), object.data...)
	if s.afterOpen != nil {
		s.afterOpen(s, key)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}
func fixtureDigester(data string) (*contentDigester, *digestFixtureSource) {
	source := &digestFixtureSource{objects: map[string]digestFixtureObject{"case": {version: contentObjectVersion{VersionID: "v1", ETag: "etag1", Size: int64(len(data)), Modified: time.Unix(100, 0)}, data: []byte(data)}}}
	return newContentDigester(source, contentDigestCacheEntries), source
}
func digestFixtureHash(data string) string {
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}
func TestContentDigesterRechecksMetadataAndStreamsOnlyOnMiss(t *testing.T) {
	d, s := fixtureDigester("old")
	first, err := d.digest(context.Background(), "case")
	require.NoError(t, err)
	require.Equal(t, digestFixtureHash("old"), first)
	second, err := d.digest(context.Background(), "case")
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, 1, s.opens)
	require.Equal(t, 3, s.stats, "first read has pre/post HEAD; every hit has a fresh HEAD")
	require.Equal(t, "v1", s.openedVersion.VersionID)
}
func TestContentDigesterSameSizeNewETagInvalidates(t *testing.T) {
	d, s := fixtureDigester("old")
	ctx := context.Background()
	first, err := d.digest(ctx, "case")
	require.NoError(t, err)
	object := s.objects["case"]
	object.data = []byte("new")
	object.version.ETag = "etag2"
	s.objects["case"] = object
	second, err := d.digest(ctx, "case")
	require.NoError(t, err)
	require.NotEqual(t, first, second)
	require.Equal(t, digestFixtureHash("new"), second)
	require.Equal(t, 2, s.opens)
}
func TestContentDigesterAllVersionFieldsParticipate(t *testing.T) {
	changes := map[string]func(*digestFixtureObject){
		"version ID": func(o *digestFixtureObject) { o.version.VersionID = "v2" },
		"modified":   func(o *digestFixtureObject) { o.version.Modified = o.version.Modified.Add(time.Second) },
		"size":       func(o *digestFixtureObject) { o.data = []byte("longer"); o.version.Size = int64(len(o.data)) },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			d, s := fixtureDigester("old")
			_, err := d.digest(context.Background(), "case")
			require.NoError(t, err)
			object := s.objects["case"]
			change(&object)
			s.objects["case"] = object
			_, err = d.digest(context.Background(), "case")
			require.NoError(t, err)
			require.Equal(t, 2, s.opens)
		})
	}
}
func TestContentDigesterFailsOnReadVersionRaceWithoutCaching(t *testing.T) {
	d, s := fixtureDigester("old")
	s.afterOpen = func(s *digestFixtureSource, key string) {
		o := s.objects[key]
		o.version.ETag = "changed-during-read"
		o.data = []byte("new")
		s.objects[key] = o
		s.afterOpen = nil
	}
	_, err := d.digest(context.Background(), "case")
	require.ErrorContains(t, err, "changed while hashing")
	require.Empty(t, d.entries)
	actual, err := d.digest(context.Background(), "case")
	require.NoError(t, err)
	require.Equal(t, digestFixtureHash("new"), actual)
	require.Equal(t, 2, s.opens)
}
func TestContentDigesterRejectsSizeMismatchAndUnversionedObject(t *testing.T) {
	d, s := fixtureDigester("old")
	object := s.objects["case"]
	object.version.Size = 2
	s.objects["case"] = object
	_, err := d.digest(context.Background(), "case")
	require.ErrorContains(t, err, "size changed")
	require.Empty(t, d.entries)
	object.version.Size = 3
	object.version.VersionID = "null"
	object.version.ETag = ""
	s.objects["case"] = object
	_, err = d.digest(context.Background(), "case")
	require.ErrorContains(t, err, "stable version metadata")
	require.Equal(t, 1, s.opens)
}
func TestContentDigesterBoundedCacheAndConcurrentReads(t *testing.T) {
	d, s := fixtureDigester("old")
	d.capacity = 2
	object := s.objects["case"]
	s.objects["second"] = object
	s.objects["third"] = object
	for _, key := range []string{"case", "second", "third"} {
		_, err := d.digest(context.Background(), key)
		require.NoError(t, err)
	}
	require.Len(t, d.entries, 2)
	_, err := d.digest(context.Background(), "case")
	require.NoError(t, err)
	require.Equal(t, 4, s.opens)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := d.digest(context.Background(), "case"); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	require.Len(t, d.entries, 2)
	require.Equal(t, 4, s.opens)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = d.digest(ctx, "case")
	require.ErrorIs(t, err, context.Canceled)
}
func TestDownloadFileLimitedEnforcesStreamingSampleLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/fixtures/sample", r.URL.Path)
		w.Header().Set("Content-Length", "10")
		w.Header().Set("Last-Modified", time.Unix(100, 0).UTC().Format(http.TimeFormat))
		w.Header().Set("ETag", "\"fixture\"")
		_, _ = io.WriteString(w, "0123456789")
	}))
	defer server.Close()
	client, err := miniogo.New(strings.TrimPrefix(server.URL, "http://"), &miniogo.Options{Secure: false, Region: "us-east-1", BucketLookup: miniogo.BucketLookupPath})
	require.NoError(t, err)
	mc := &MinIOClient{client: client, bucket: "fixtures"}
	_, err = mc.DownloadFileLimited(context.Background(), "sample", 4)
	require.ErrorContains(t, err, "exceeds 4")
	data, err := mc.DownloadFileLimited(context.Background(), "sample", 10)
	require.NoError(t, err)
	require.Equal(t, "0123456789", string(data))
}

func TestContentDigesterMinIOPinsVersionAndChecksCacheWithHEAD(t *testing.T) {
	var mu sync.Mutex
	gets, heads := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Length", "3")
		w.Header().Set("Last-Modified", time.Unix(100, 0).UTC().Format(http.TimeFormat))
		w.Header().Set("ETag", string([]byte{34})+"etag-1"+string([]byte{34}))
		w.Header().Set("X-Amz-Version-Id", "version-1")
		if r.Method == http.MethodHead {
			heads++
			return
		}
		gets++
		require.Equal(t, "version-1", r.URL.Query().Get("versionId"))
		require.Equal(t, string([]byte{34})+"etag-1"+string([]byte{34}), r.Header.Get("If-Match"))
		_, _ = io.WriteString(w, "old")
	}))
	defer server.Close()
	client, err := miniogo.New(strings.TrimPrefix(server.URL, "http://"), &miniogo.Options{Secure: false, Region: "us-east-1", BucketLookup: miniogo.BucketLookupPath})
	require.NoError(t, err)
	digest := NewContentDigester(&MinIOClient{client: client, bucket: "fixtures"})
	for i := 0; i < 2; i++ {
		value, err := digest(context.Background(), "case")
		require.NoError(t, err)
		require.Equal(t, digestFixtureHash("old"), value)
	}
	require.Equal(t, 1, gets)
	require.Equal(t, 3, heads)
}
