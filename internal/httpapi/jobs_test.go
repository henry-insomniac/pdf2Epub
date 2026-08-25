package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"pdf2epub/internal/app"
	"pdf2epub/internal/auth"
	"pdf2epub/internal/domain"
)

func TestProtectedJobLifecycle(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	converter := converterStub(func(ctx context.Context, _ app.ConversionRequest, _ app.Reporter) (app.ConversionResult, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return app.ConversionResult{}, ctx.Err()
	})
	jobs := newTestManager(t, converter)
	server, client := newAuthenticatedServer(t, jobs)

	response := uploadPDF(t, client, server.URL, "book.pdf", []byte("%PDF-1.7\nfixture"), "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous upload status = %d, want 401", response.StatusCode)
	}
	response.Body.Close()

	csrf := login(t, client, server.URL)
	response = uploadPDF(t, client, server.URL, "book.pdf", []byte("%PDF-1.7\nfixture"), "")
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("upload without CSRF status = %d, want 403", response.StatusCode)
	}
	response.Body.Close()

	response = uploadPDF(t, client, server.URL, "book.pdf", []byte("%PDF-1.7\nfixture"), csrf)
	if response.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("upload status = %d, want 202: %s", response.StatusCode, body)
	}
	var created domain.Snapshot
	decodeJSON(t, response, &created)
	<-started

	response = uploadPDF(t, client, server.URL, "second.pdf", []byte("%PDF-1.7\nfixture"), csrf)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("second upload status = %d, want 409", response.StatusCode)
	}
	response.Body.Close()

	response = doJSON(t, client, http.MethodGet, server.URL+"/api/v1/jobs/"+created.ID, nil, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get job status = %d, want 200", response.StatusCode)
	}
	response.Body.Close()

	response = doJSON(t, client, http.MethodPost, server.URL+"/api/v1/jobs/"+created.ID+"/cancel", nil, csrf)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel status = %d, want 204", response.StatusCode)
	}
	response.Body.Close()

	response = doJSON(t, client, http.MethodGet, server.URL+"/api/v1/jobs/"+created.ID, nil, "")
	var canceled domain.Snapshot
	decodeJSON(t, response, &canceled)
	if canceled.Status != domain.JobCanceled {
		t.Fatalf("status = %q, want canceled", canceled.Status)
	}
}

func TestSuccessfulJobCanBeDownloaded(t *testing.T) {
	converter := converterStub(func(_ context.Context, request app.ConversionRequest, _ app.Reporter) (app.ConversionResult, error) {
		path := filepath.Join(request.JobDir, "book.epub")
		if err := os.WriteFile(path, []byte("epub fixture"), 0o600); err != nil {
			return app.ConversionResult{}, err
		}
		return app.ConversionResult{Artifact: domain.Artifact{Name: "book.epub", Path: path, Size: 12}}, nil
	})
	jobs := newTestManager(t, converter)
	server, client := newAuthenticatedServer(t, jobs)
	csrf := login(t, client, server.URL)

	response := uploadPDF(t, client, server.URL, "book.pdf", []byte("%PDF-1.7\nfixture"), csrf)
	var created domain.Snapshot
	decodeJSON(t, response, &created)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response = doJSON(t, client, http.MethodGet, server.URL+"/api/v1/jobs/"+created.ID, nil, "")
		var snapshot domain.Snapshot
		decodeJSON(t, response, &snapshot)
		if snapshot.Status == domain.JobSucceeded {
			break
		}
		time.Sleep(time.Millisecond)
	}
	response = doJSON(t, client, http.MethodGet, server.URL+"/api/v1/jobs/"+created.ID+"/download", nil, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("download status = %d, want 200", response.StatusCode)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read download: %v", err)
	}
	if string(content) != "epub fixture" {
		t.Fatalf("download = %q", content)
	}
	if !strings.Contains(response.Header.Get("Content-Disposition"), "book.epub") {
		t.Fatalf("Content-Disposition = %q", response.Header.Get("Content-Disposition"))
	}
}

func TestSuccessfulRemoteJobRedirectsToSignedDownload(t *testing.T) {
	converter := converterStub(func(_ context.Context, request app.ConversionRequest, _ app.Reporter) (app.ConversionResult, error) {
		path := filepath.Join(request.JobDir, "book.epub")
		if err := os.WriteFile(path, []byte("epub fixture"), 0o600); err != nil {
			return app.ConversionResult{}, err
		}
		return app.ConversionResult{Artifact: domain.Artifact{Name: "book.epub", Path: path, Size: 12}}, nil
	})
	jobs, err := app.NewManager(app.ManagerConfig{
		WorkDir:        t.TempDir(),
		MaxUploadBytes: 1024,
		JobTimeout:     time.Second,
		Retention:      time.Hour,
		ArtifactStore:  remoteStoreStub{},
	}, converter)
	if err != nil {
		t.Fatalf("app.NewManager(): %v", err)
	}
	t.Cleanup(jobs.Close)
	server, client := newAuthenticatedServer(t, jobs)
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	csrf := login(t, client, server.URL)

	response := uploadPDF(t, client, server.URL, "book.pdf", []byte("%PDF-1.7\nfixture"), csrf)
	var created domain.Snapshot
	decodeJSON(t, response, &created)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response = doJSON(t, client, http.MethodGet, server.URL+"/api/v1/jobs/"+created.ID, nil, "")
		var snapshot domain.Snapshot
		decodeJSON(t, response, &snapshot)
		if snapshot.Status == domain.JobSucceeded {
			break
		}
		time.Sleep(time.Millisecond)
	}
	response = doJSON(t, client, http.MethodGet, server.URL+"/api/v1/jobs/"+created.ID+"/download", nil, "")
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("download status = %d, want 302", response.StatusCode)
	}
	if response.Header.Get("Location") != "https://objects.example/book.epub?signature=test" {
		t.Fatalf("Location = %q", response.Header.Get("Location"))
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header.Get("Cache-Control"))
	}
}

func TestUploadRejectsNonPDF(t *testing.T) {
	jobs := newTestManager(t, converterStub(func(context.Context, app.ConversionRequest, app.Reporter) (app.ConversionResult, error) {
		return app.ConversionResult{}, nil
	}))
	server, client := newAuthenticatedServer(t, jobs)
	csrf := login(t, client, server.URL)

	response := uploadPDF(t, client, server.URL, "notes.txt", []byte("not a pdf"), csrf)
	if response.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", response.StatusCode)
	}
	response.Body.Close()
}

func TestUploadPassesRequestedConversionMode(t *testing.T) {
	received := make(chan app.ConversionMode, 1)
	converter := converterStub(func(ctx context.Context, request app.ConversionRequest, _ app.Reporter) (app.ConversionResult, error) {
		received <- request.Mode
		<-ctx.Done()
		return app.ConversionResult{}, ctx.Err()
	})
	jobs := newTestManager(t, converter)
	server, client := newAuthenticatedServer(t, jobs)
	csrf := login(t, client, server.URL)

	response := uploadPDFMode(t, client, server.URL, "book.pdf", []byte("%PDF-1.7\nfixture"), "fixed", csrf)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", response.StatusCode)
	}
	var created domain.Snapshot
	decodeJSON(t, response, &created)
	if mode := <-received; mode != app.ConversionModeFixed {
		t.Fatalf("conversion mode = %q, want fixed", mode)
	}
	if err := jobs.Cancel(created.ID); err != nil {
		t.Fatalf("cancel job: %v", err)
	}
}

func TestUploadRejectsInvalidConversionMode(t *testing.T) {
	jobs := newTestManager(t, converterStub(func(context.Context, app.ConversionRequest, app.Reporter) (app.ConversionResult, error) {
		return app.ConversionResult{}, errors.New("converter must not run")
	}))
	server, client := newAuthenticatedServer(t, jobs)
	csrf := login(t, client, server.URL)

	response := uploadPDFMode(t, client, server.URL, "book.pdf", []byte("%PDF-1.7\nfixture"), "sideways", csrf)
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
}

func newTestManager(t *testing.T, converter converterStub) *app.Manager {
	t.Helper()
	manager, err := app.NewManager(app.ManagerConfig{
		WorkDir:        t.TempDir(),
		MaxUploadBytes: 1024,
		JobTimeout:     time.Second,
		Retention:      time.Hour,
	}, converter)
	if err != nil {
		t.Fatalf("app.NewManager(): %v", err)
	}
	t.Cleanup(manager.Close)
	return manager
}

func newAuthenticatedServer(t *testing.T, jobs *app.Manager) (*httptest.Server, *http.Client) {
	t.Helper()
	sessions := auth.NewManager("admin", "secret password", time.Hour)
	server := httptest.NewServer(New(sessions, jobs, 1024, false).Handler())
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New(): %v", err)
	}
	return server, &http.Client{Jar: jar}
}

func login(t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()
	response := doJSON(t, client, http.MethodPost, baseURL+"/api/v1/auth/login", map[string]string{
		"username": "admin",
		"password": "secret password",
	}, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", response.StatusCode)
	}
	var body sessionResponse
	decodeJSON(t, response, &body)
	return body.CSRFToken
}

func uploadPDF(t *testing.T, client *http.Client, baseURL, name string, content []byte, csrf string) *http.Response {
	return uploadPDFMode(t, client, baseURL, name, content, "", csrf)
}

func uploadPDFMode(t *testing.T, client *http.Client, baseURL, name string, content []byte, mode, csrf string) *http.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if mode != "" {
		if err := writer.WriteField("mode", mode); err != nil {
			t.Fatalf("WriteField(mode): %v", err)
		}
	}
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatalf("CreateFormFile(): %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write multipart content: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/jobs", &body)
	if err != nil {
		t.Fatalf("http.NewRequest(): %v", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("client.Do(): %v", err)
	}
	return response
}

type converterStub func(context.Context, app.ConversionRequest, app.Reporter) (app.ConversionResult, error)

func (fn converterStub) Convert(ctx context.Context, request app.ConversionRequest, reporter app.Reporter) (app.ConversionResult, error) {
	return fn(ctx, request, reporter)
}

type remoteStoreStub struct{}

func (remoteStoreStub) Publish(_ context.Context, jobID string, _ domain.Artifact) (string, error) {
	return "epub/" + jobID + ".epub", nil
}

func (remoteStoreStub) SignedDownloadURL(context.Context, domain.Artifact, time.Duration) (string, error) {
	return "https://objects.example/book.epub?signature=test", nil
}

func (remoteStoreStub) Delete(context.Context, string) error {
	return nil
}
