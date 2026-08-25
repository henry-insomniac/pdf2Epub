package artifactstore

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"pdf2epub/internal/domain"
)

func TestR2StorePublishesSignsAndDeletesArtifact(t *testing.T) {
	var mu sync.Mutex
	methods := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		mu.Lock()
		methods = append(methods, request.Method)
		mu.Unlock()
		if request.URL.Path != "/books/epub/job-123.epub" {
			t.Errorf("request path = %q", request.URL.Path)
		}
		switch request.Method {
		case http.MethodPut:
			content, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read upload: %v", err)
			}
			if string(content) != "epub fixture" {
				t.Errorf("upload content = %q", content)
			}
			if request.Header.Get("Content-Type") != "application/epub+zip" {
				t.Errorf("Content-Type = %q", request.Header.Get("Content-Type"))
			}
			if !strings.Contains(request.Header.Get("Content-Disposition"), "filename*=utf-8''") {
				t.Errorf("Content-Disposition = %q", request.Header.Get("Content-Disposition"))
			}
			w.WriteHeader(http.StatusOK)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)

	store, err := NewR2(Config{
		Endpoint:        server.URL,
		AccessKeyID:     "access-key",
		SecretAccessKey: "secret-key",
		Bucket:          "books",
		Prefix:          "epub",
		HTTPClient:      server.Client(),
		AllowHTTP:       true,
	})
	if err != nil {
		t.Fatalf("NewR2(): %v", err)
	}
	artifactPath := filepath.Join(t.TempDir(), "中文书名.epub")
	if err := os.WriteFile(artifactPath, []byte("epub fixture"), 0o600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	artifact := domain.Artifact{Name: "中文书名.epub", Path: artifactPath, Size: 12}
	key, err := store.Publish(context.Background(), "job-123", artifact)
	if err != nil {
		t.Fatalf("Publish(): %v", err)
	}
	if key != "epub/job-123.epub" {
		t.Fatalf("key = %q", key)
	}

	artifact.StorageKey = key
	downloadURL, err := store.SignedDownloadURL(context.Background(), artifact, 10*time.Minute)
	if err != nil {
		t.Fatalf("SignedDownloadURL(): %v", err)
	}
	parsed, err := url.Parse(downloadURL)
	if err != nil {
		t.Fatalf("parse download URL: %v", err)
	}
	if parsed.Path != "/books/epub/job-123.epub" || parsed.Query().Get("X-Amz-Expires") != "600" {
		t.Fatalf("signed URL = %q", downloadURL)
	}
	if parsed.Query().Get("X-Amz-Signature") == "" {
		t.Fatalf("signed URL has no signature: %q", downloadURL)
	}

	if err := store.Delete(context.Background(), key); err != nil {
		t.Fatalf("Delete(): %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(methods) != 2 || methods[0] != http.MethodPut || methods[1] != http.MethodDelete {
		t.Fatalf("methods = %v", methods)
	}
}

func TestR2StoreRejectsUnsafeJobID(t *testing.T) {
	store, err := NewR2(Config{
		Endpoint:        "https://example.invalid",
		AccessKeyID:     "access-key",
		SecretAccessKey: "secret-key",
		Bucket:          "books",
	})
	if err != nil {
		t.Fatalf("NewR2(): %v", err)
	}
	_, err = store.Publish(context.Background(), "../job", domain.Artifact{Name: "book.epub", Path: "/tmp/book.epub", Size: 1})
	if err == nil {
		t.Fatal("Publish() error = nil, want unsafe key rejection")
	}
}
