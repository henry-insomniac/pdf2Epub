package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"pdf2epub/internal/domain"
)

func TestManagerRunsOneJobAndRetainsArtifact(t *testing.T) {
	root := t.TempDir()
	release := make(chan struct{})
	converter := converterFunc(func(ctx context.Context, request ConversionRequest, reporter Reporter) (ConversionResult, error) {
		reporter.SetTotalPages(3)
		reporter.Progress(domain.StageExtracting, 2)
		<-release
		artifactPath := filepath.Join(request.JobDir, "book.epub")
		if err := os.WriteFile(artifactPath, []byte("epub"), 0o600); err != nil {
			return ConversionResult{}, err
		}
		return ConversionResult{
			Artifact: domain.Artifact{Name: "book.epub", Path: artifactPath, Size: 4},
			Warnings: []domain.Warning{{Code: "content.table_as_image", Message: "table rendered"}},
		}, nil
	})
	manager, err := NewManager(ManagerConfig{
		WorkDir:        root,
		MaxUploadBytes: 1024,
		JobTimeout:     time.Second,
		Retention:      time.Hour,
	}, converter)
	if err != nil {
		t.Fatalf("NewManager(): %v", err)
	}
	t.Cleanup(manager.Close)

	first, err := manager.Submit("book.pdf", bytes.NewBufferString("%PDF test"))
	if err != nil {
		t.Fatalf("Submit(): %v", err)
	}
	if _, err := manager.Submit("second.pdf", bytes.NewBufferString("%PDF test")); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Submit() error = %v, want ErrBusy", err)
	}
	close(release)

	finished := waitForTerminal(t, manager, first.ID)
	if finished.Status != domain.JobSucceeded {
		t.Fatalf("status = %q, want succeeded; failure=%#v", finished.Status, finished.Failure)
	}
	if finished.TotalPages != 3 || finished.ProcessedPages != 2 {
		t.Fatalf("progress = %d/%d", finished.ProcessedPages, finished.TotalPages)
	}
	if len(finished.Warnings) != 1 {
		t.Fatalf("warnings = %d, want 1", len(finished.Warnings))
	}
	if _, err := os.Stat(finished.Artifact.Path); err != nil {
		t.Fatalf("artifact stat: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, first.ID, "source.pdf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source file still exists or unexpected error: %v", err)
	}
}

func TestManagerPublishesArtifactAndReturnsSignedDownload(t *testing.T) {
	root := t.TempDir()
	store := &artifactStoreStub{downloadURL: "https://objects.example/download.epub?signature=test"}
	manager, err := NewManager(ManagerConfig{
		WorkDir:        root,
		MaxUploadBytes: 1024,
		JobTimeout:     time.Second,
		Retention:      time.Hour,
		DownloadURLTTL: 10 * time.Minute,
		ArtifactStore:  store,
	}, converterFunc(func(_ context.Context, request ConversionRequest, _ Reporter) (ConversionResult, error) {
		artifactPath := filepath.Join(request.JobDir, "中文书名.epub")
		if err := os.WriteFile(artifactPath, []byte("epub"), 0o600); err != nil {
			return ConversionResult{}, err
		}
		return ConversionResult{Artifact: domain.Artifact{Name: "中文书名.epub", Path: artifactPath, Size: 4}}, nil
	}))
	if err != nil {
		t.Fatalf("NewManager(): %v", err)
	}
	t.Cleanup(manager.Close)

	job, err := manager.Submit("中文书名.pdf", bytes.NewBufferString("%PDF test"))
	if err != nil {
		t.Fatalf("Submit(): %v", err)
	}
	finished := waitForTerminal(t, manager, job.ID)
	if finished.Status != domain.JobSucceeded || finished.Artifact == nil {
		t.Fatalf("snapshot = %#v", finished)
	}
	if finished.Artifact.StorageKey != "epub/"+job.ID+".epub" || finished.Artifact.Path != "" {
		t.Fatalf("artifact = %#v", finished.Artifact)
	}
	if _, err := os.Stat(filepath.Join(root, job.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remote job directory still exists or unexpected error: %v", err)
	}
	download, err := manager.Download(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Download(): %v", err)
	}
	if download.URL != store.downloadURL || download.Artifact.Name != "中文书名.epub" {
		t.Fatalf("download = %#v", download)
	}
	if store.lastTTL != 10*time.Minute {
		t.Fatalf("signed URL TTL = %s", store.lastTTL)
	}
}

func TestManagerFallsBackToLocalDownloadWhenPublishFails(t *testing.T) {
	manager, err := NewManager(ManagerConfig{
		WorkDir:        t.TempDir(),
		MaxUploadBytes: 1024,
		JobTimeout:     time.Second,
		Retention:      time.Hour,
		ArtifactStore:  &artifactStoreStub{publishErr: errors.New("R2 unavailable")},
	}, converterFunc(func(_ context.Context, request ConversionRequest, _ Reporter) (ConversionResult, error) {
		artifactPath := filepath.Join(request.JobDir, "book.epub")
		if err := os.WriteFile(artifactPath, []byte("epub"), 0o600); err != nil {
			return ConversionResult{}, err
		}
		return ConversionResult{Artifact: domain.Artifact{Name: "book.epub", Path: artifactPath, Size: 4}}, nil
	}))
	if err != nil {
		t.Fatalf("NewManager(): %v", err)
	}
	t.Cleanup(manager.Close)

	job, err := manager.Submit("book.pdf", bytes.NewBufferString("%PDF test"))
	if err != nil {
		t.Fatalf("Submit(): %v", err)
	}
	finished := waitForTerminal(t, manager, job.ID)
	if finished.Status != domain.JobSucceeded || finished.Artifact == nil || finished.Artifact.Path == "" {
		t.Fatalf("snapshot = %#v", finished)
	}
	if len(finished.Warnings) != 1 || finished.Warnings[0].Code != "delivery.object_store_fallback" {
		t.Fatalf("warnings = %#v", finished.Warnings)
	}
	download, err := manager.Download(context.Background(), job.ID)
	if err != nil || download.URL != "" {
		t.Fatalf("Download() = %#v, %v", download, err)
	}
}

func TestManagerCancelsAndReleasesSlot(t *testing.T) {
	root := t.TempDir()
	started := make(chan struct{})
	var signalStarted sync.Once
	converter := converterFunc(func(ctx context.Context, _ ConversionRequest, _ Reporter) (ConversionResult, error) {
		signalStarted.Do(func() { close(started) })
		<-ctx.Done()
		return ConversionResult{}, ctx.Err()
	})
	manager, err := NewManager(ManagerConfig{
		WorkDir:        root,
		MaxUploadBytes: 1024,
		JobTimeout:     time.Second,
		Retention:      time.Hour,
	}, converter)
	if err != nil {
		t.Fatalf("NewManager(): %v", err)
	}
	t.Cleanup(manager.Close)

	job, err := manager.Submit("book.pdf", bytes.NewBufferString("%PDF test"))
	if err != nil {
		t.Fatalf("Submit(): %v", err)
	}
	<-started
	if err := manager.Cancel(job.ID); err != nil {
		t.Fatalf("Cancel(): %v", err)
	}
	finished := waitForTerminal(t, manager, job.ID)
	if finished.Status != domain.JobCanceled {
		t.Fatalf("status = %q, want canceled", finished.Status)
	}

	if _, err := manager.Submit("next.pdf", bytes.NewBufferString("%PDF next")); err != nil {
		t.Fatalf("Submit() after cancel: %v", err)
	}
}

func TestManagerTimesOutJob(t *testing.T) {
	manager, err := NewManager(ManagerConfig{
		WorkDir:        t.TempDir(),
		MaxUploadBytes: 1024,
		JobTimeout:     20 * time.Millisecond,
		Retention:      time.Hour,
	}, converterFunc(func(ctx context.Context, _ ConversionRequest, _ Reporter) (ConversionResult, error) {
		<-ctx.Done()
		return ConversionResult{}, ctx.Err()
	}))
	if err != nil {
		t.Fatalf("NewManager(): %v", err)
	}
	t.Cleanup(manager.Close)

	job, err := manager.Submit("book.pdf", bytes.NewBufferString("%PDF test"))
	if err != nil {
		t.Fatalf("Submit(): %v", err)
	}
	finished := waitForTerminal(t, manager, job.ID)
	if finished.Status != domain.JobFailed || finished.Failure == nil || finished.Failure.Code != "job.timeout" {
		t.Fatalf("snapshot = %#v, want timeout failure", finished)
	}
}

func TestManagerRejectsOversizedUpload(t *testing.T) {
	manager, err := NewManager(ManagerConfig{
		WorkDir:        t.TempDir(),
		MaxUploadBytes: 4,
		JobTimeout:     time.Second,
		Retention:      time.Hour,
	}, converterFunc(func(context.Context, ConversionRequest, Reporter) (ConversionResult, error) {
		return ConversionResult{}, errors.New("must not run")
	}))
	if err != nil {
		t.Fatalf("NewManager(): %v", err)
	}
	t.Cleanup(manager.Close)

	if _, err := manager.Submit("book.pdf", bytes.NewBufferString("12345")); !errors.Is(err, ErrUploadTooLarge) {
		t.Fatalf("Submit() error = %v, want ErrUploadTooLarge", err)
	}
}

func waitForTerminal(t *testing.T, manager *Manager, id string) domain.Snapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, ok := manager.Get(id)
		if !ok {
			t.Fatalf("job %q disappeared", id)
		}
		if snapshot.Status == domain.JobSucceeded || snapshot.Status == domain.JobFailed || snapshot.Status == domain.JobCanceled {
			return snapshot
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("job %q did not finish", id)
	return domain.Snapshot{}
}

type converterFunc func(context.Context, ConversionRequest, Reporter) (ConversionResult, error)

func (fn converterFunc) Convert(ctx context.Context, request ConversionRequest, reporter Reporter) (ConversionResult, error) {
	return fn(ctx, request, reporter)
}

type artifactStoreStub struct {
	publishErr  error
	downloadURL string
	lastTTL     time.Duration
}

func (s *artifactStoreStub) Publish(_ context.Context, jobID string, _ domain.Artifact) (string, error) {
	if s.publishErr != nil {
		return "", s.publishErr
	}
	return "epub/" + jobID + ".epub", nil
}

func (s *artifactStoreStub) SignedDownloadURL(_ context.Context, _ domain.Artifact, ttl time.Duration) (string, error) {
	s.lastTTL = ttl
	return s.downloadURL, nil
}

func (s *artifactStoreStub) Delete(context.Context, string) error {
	return nil
}
