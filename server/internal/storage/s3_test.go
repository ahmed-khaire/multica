package storage

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNewS3StorageFromEnvSupportsS3CompatibleEndpoint(t *testing.T) {
	t.Setenv("S3_BUCKET", "multica-local")
	t.Setenv("S3_REGION", "us-east-1")
	t.Setenv("S3_ENDPOINT", "http://127.0.0.1:9000/")
	t.Setenv("AWS_ACCESS_KEY_ID", "minio")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "minio-secret")
	t.Setenv("CLOUDFRONT_DOMAIN", "")

	store := NewS3StorageFromEnv()
	if store == nil {
		t.Fatal("NewS3StorageFromEnv returned nil")
	}
	if store.baseURL != "http://127.0.0.1:9000" {
		t.Fatalf("baseURL = %q, want trimmed S3_ENDPOINT", store.baseURL)
	}

	got := store.KeyFromURL("http://127.0.0.1:9000/multica-local/uploads/test.png")
	if got != "uploads/test.png" {
		t.Fatalf("KeyFromURL returned %q, want uploads/test.png", got)
	}
}

func TestS3StorageUploadAgainstConfiguredEndpoint(t *testing.T) {
	if os.Getenv("MULTICA_TEST_S3_INTEGRATION") != "1" {
		t.Skip("set MULTICA_TEST_S3_INTEGRATION=1 to run against local S3/MinIO")
	}

	store := NewS3StorageFromEnv()
	if store == nil {
		t.Fatal("NewS3StorageFromEnv returned nil")
	}

	key := "e2e/test-" + time.Now().Format("20060102150405.000000000") + ".txt"
	url, err := store.Upload(
		context.Background(),
		key,
		[]byte("hello from multica storage integration test"),
		"text/plain",
		"test.txt",
	)
	if err != nil {
		t.Fatalf("Upload returned error: %v", err)
	}
	t.Cleanup(func() {
		store.Delete(context.Background(), key)
	})

	if !strings.Contains(url, key) {
		t.Fatalf("Upload URL %q does not contain key %q", url, key)
	}
}
