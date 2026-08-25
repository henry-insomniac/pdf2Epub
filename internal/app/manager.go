package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"pdf2epub/internal/domain"
)

var (
	ErrBusy                = errors.New("a conversion job is already active")
	ErrInvalidMode         = errors.New("invalid conversion mode")
	ErrJobNotFound         = errors.New("job not found")
	ErrNotCancelable       = errors.New("job cannot be canceled")
	ErrUploadTooLarge      = errors.New("upload exceeds configured limit")
	ErrArtifactUnavailable = errors.New("artifact is not available")
)

type ConversionMode string

const (
	ConversionModeAuto       ConversionMode = "auto"
	ConversionModeReflowable ConversionMode = "reflowable"
	ConversionModeFixed      ConversionMode = "fixed"
)

func ParseConversionMode(value string) (ConversionMode, error) {
	mode := ConversionMode(strings.ToLower(strings.TrimSpace(value)))
	if mode == "" {
		return ConversionModeAuto, nil
	}
	switch mode {
	case ConversionModeAuto, ConversionModeReflowable, ConversionModeFixed:
		return mode, nil
	default:
		return "", ErrInvalidMode
	}
}

type ManagerConfig struct {
	WorkDir           string
	MaxUploadBytes    int64
	JobTimeout        time.Duration
	Retention         time.Duration
	DownloadURLTTL    time.Duration
	ArtifactStore     ArtifactStore
	StoreCleanupLimit time.Duration
}

type ConversionRequest struct {
	SourcePath string
	SourceName string
	JobDir     string
	Mode       ConversionMode
}

type ConversionResult struct {
	Artifact domain.Artifact
	Warnings []domain.Warning
}

type Reporter interface {
	SetTotalPages(total int)
	Progress(stage domain.Stage, processedPages int)
}

type Converter interface {
	Convert(context.Context, ConversionRequest, Reporter) (ConversionResult, error)
}

type ArtifactStore interface {
	Publish(context.Context, string, domain.Artifact) (string, error)
	SignedDownloadURL(context.Context, domain.Artifact, time.Duration) (string, error)
	Delete(context.Context, string) error
}

type ArtifactDownload struct {
	Artifact domain.Artifact
	URL      string
}

type ConversionFailure struct {
	Failure domain.Failure
}

func (e ConversionFailure) Error() string {
	return e.Failure.Message
}

type managedJob struct {
	job        *domain.Job
	jobDir     string
	sourcePath string
	cancel     context.CancelFunc
	done       chan struct{}
	mode       ConversionMode
}

type Manager struct {
	mu        sync.RWMutex
	config    ManagerConfig
	converter Converter
	jobs      map[string]*managedJob
	activeID  string
	closed    bool
	wg        sync.WaitGroup
}

func NewManager(config ManagerConfig, converter Converter) (*Manager, error) {
	if config.WorkDir == "" || !filepath.IsAbs(config.WorkDir) {
		return nil, errors.New("work directory must be an absolute path")
	}
	cleanWorkDir := filepath.Clean(config.WorkDir)
	if cleanWorkDir == string(filepath.Separator) || cleanWorkDir == filepath.VolumeName(cleanWorkDir)+string(filepath.Separator) {
		return nil, errors.New("work directory cannot be a filesystem root")
	}
	config.WorkDir = cleanWorkDir
	if config.MaxUploadBytes <= 0 || config.JobTimeout <= 0 || config.Retention <= 0 {
		return nil, errors.New("manager limits must be positive")
	}
	if config.DownloadURLTTL <= 0 {
		config.DownloadURLTTL = 15 * time.Minute
	}
	if config.StoreCleanupLimit <= 0 {
		config.StoreCleanupLimit = 15 * time.Second
	}
	if converter == nil {
		return nil, errors.New("converter is required")
	}
	if err := os.MkdirAll(config.WorkDir, 0o700); err != nil {
		return nil, fmt.Errorf("create work directory: %w", err)
	}
	entries, err := os.ReadDir(config.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("read work directory: %w", err)
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(config.WorkDir, entry.Name())); err != nil {
			return nil, fmt.Errorf("clean stale work item %q: %w", entry.Name(), err)
		}
	}
	return &Manager{
		config:    config,
		converter: converter,
		jobs:      make(map[string]*managedJob),
	}, nil
}

func (m *Manager) Submit(sourceName string, mode ConversionMode, input io.Reader) (domain.Snapshot, error) {
	mode, err := ParseConversionMode(string(mode))
	if err != nil {
		return domain.Snapshot{}, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return domain.Snapshot{}, errors.New("manager is closed")
	}
	if m.activeID != "" {
		m.mu.Unlock()
		return domain.Snapshot{}, ErrBusy
	}
	id, err := randomID()
	if err != nil {
		m.mu.Unlock()
		return domain.Snapshot{}, fmt.Errorf("create job id: %w", err)
	}
	jobDir := filepath.Join(m.config.WorkDir, id)
	if err := os.Mkdir(jobDir, 0o700); err != nil {
		m.mu.Unlock()
		return domain.Snapshot{}, fmt.Errorf("create job directory: %w", err)
	}
	sourcePath := filepath.Join(jobDir, "source.pdf")
	job := domain.NewJob(id, safeSourceName(sourceName), 0)
	managed := &managedJob{job: job, jobDir: jobDir, sourcePath: sourcePath, done: make(chan struct{}), mode: mode}
	m.jobs[id] = managed
	m.activeID = id
	m.mu.Unlock()

	if err := writeLimitedFile(sourcePath, input, m.config.MaxUploadBytes); err != nil {
		_ = os.RemoveAll(jobDir)
		m.mu.Lock()
		delete(m.jobs, id)
		if m.activeID == id {
			m.activeID = ""
		}
		m.mu.Unlock()
		return domain.Snapshot{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), m.config.JobTimeout)
	m.mu.Lock()
	managed.cancel = cancel
	m.mu.Unlock()
	m.wg.Add(1)
	go m.run(ctx, managed)
	return job.Snapshot(), nil
}

func (m *Manager) Get(id string) (domain.Snapshot, bool) {
	m.mu.RLock()
	managed, ok := m.jobs[id]
	m.mu.RUnlock()
	if !ok {
		return domain.Snapshot{}, false
	}
	return managed.job.Snapshot(), true
}

func (m *Manager) Cancel(id string) error {
	m.mu.RLock()
	managed, ok := m.jobs[id]
	m.mu.RUnlock()
	if !ok {
		return ErrJobNotFound
	}
	snapshot := managed.job.Snapshot()
	if snapshot.Status != domain.JobQueued && snapshot.Status != domain.JobProcessing {
		return ErrNotCancelable
	}
	if err := managed.job.Cancel(); err != nil {
		return ErrNotCancelable
	}
	if managed.cancel != nil {
		managed.cancel()
	}
	<-managed.done
	return nil
}

func (m *Manager) Download(ctx context.Context, id string) (ArtifactDownload, error) {
	snapshot, ok := m.Get(id)
	if !ok {
		return ArtifactDownload{}, ErrJobNotFound
	}
	if snapshot.Status != domain.JobSucceeded || snapshot.Artifact == nil {
		return ArtifactDownload{}, ErrArtifactUnavailable
	}
	download := ArtifactDownload{Artifact: *snapshot.Artifact}
	if snapshot.Artifact.StorageKey == "" {
		return download, nil
	}
	if m.config.ArtifactStore == nil {
		return ArtifactDownload{}, errors.New("artifact store is not configured")
	}
	url, err := m.config.ArtifactStore.SignedDownloadURL(ctx, *snapshot.Artifact, m.config.DownloadURLTTL)
	if err != nil {
		return ArtifactDownload{}, fmt.Errorf("sign artifact download: %w", err)
	}
	download.URL = url
	return download, nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	for _, managed := range m.jobs {
		if managed.cancel != nil {
			managed.cancel()
		}
	}
	m.mu.Unlock()
	m.wg.Wait()
	m.cleanupStoredArtifacts()
	_ = os.RemoveAll(m.config.WorkDir)
}

func (m *Manager) run(ctx context.Context, managed *managedJob) {
	defer m.wg.Done()
	defer managed.cancel()
	if err := managed.job.Start(); err != nil {
		m.finish(managed)
		return
	}
	result, err := m.converter.Convert(ctx, ConversionRequest{
		SourcePath: managed.sourcePath,
		SourceName: managed.job.Snapshot().SourceName,
		JobDir:     managed.jobDir,
		Mode:       managed.mode,
	}, jobReporter{job: managed.job})

	snapshot := managed.job.Snapshot()
	if snapshot.Status == domain.JobCanceled {
		_ = os.RemoveAll(managed.jobDir)
		m.finish(managed)
		return
	}
	if err != nil {
		slog.Error("conversion job failed", "job_id", managed.job.Snapshot().ID, "error", err)
		failure := domain.Failure{Code: "conversion.failed", Message: "PDF 转换失败，请检查文件后重试。"}
		var conversionFailure ConversionFailure
		if errors.As(err, &conversionFailure) {
			failure = conversionFailure.Failure
		} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			failure = domain.Failure{Code: "job.timeout", Message: "转换超过 30 分钟，任务已停止。"}
		}
		_ = managed.job.Fail(failure)
		_ = os.RemoveAll(managed.jobDir)
		m.finish(managed)
		return
	}
	for _, warning := range result.Warnings {
		managed.job.AddWarning(warning)
	}
	if m.config.ArtifactStore != nil {
		storageKey, publishErr := m.config.ArtifactStore.Publish(ctx, managed.job.Snapshot().ID, result.Artifact)
		if publishErr != nil {
			slog.Error("publish artifact to object storage failed", "job_id", managed.job.Snapshot().ID, "error", publishErr)
			managed.job.AddWarning(domain.Warning{
				Code:    "delivery.object_store_fallback",
				Message: "云端下载暂时不可用，本次使用源站下载。",
			})
		} else {
			result.Artifact.StorageKey = storageKey
			result.Artifact.Path = ""
			_ = os.RemoveAll(managed.jobDir)
		}
	}
	_ = os.Remove(managed.sourcePath)
	if err := managed.job.Succeed(result.Artifact); err != nil {
		m.deleteStoredArtifact(result.Artifact)
		_ = os.RemoveAll(managed.jobDir)
		m.finish(managed)
		return
	}
	m.finish(managed)
}

func (m *Manager) finish(managed *managedJob) {
	id := managed.job.Snapshot().ID
	m.mu.Lock()
	if m.activeID == id {
		m.activeID = ""
	}
	m.mu.Unlock()
	close(managed.done)
	time.AfterFunc(m.config.Retention, func() {
		snapshot := managed.job.Snapshot()
		if snapshot.Artifact != nil {
			m.deleteStoredArtifact(*snapshot.Artifact)
		}
		_ = os.RemoveAll(managed.jobDir)
		m.mu.Lock()
		delete(m.jobs, id)
		m.mu.Unlock()
	})
}

func (m *Manager) cleanupStoredArtifacts() {
	if m.config.ArtifactStore == nil {
		return
	}
	m.mu.RLock()
	artifacts := make([]domain.Artifact, 0, len(m.jobs))
	for _, managed := range m.jobs {
		snapshot := managed.job.Snapshot()
		if snapshot.Artifact != nil && snapshot.Artifact.StorageKey != "" {
			artifacts = append(artifacts, *snapshot.Artifact)
		}
	}
	m.mu.RUnlock()
	for _, artifact := range artifacts {
		m.deleteStoredArtifact(artifact)
	}
}

func (m *Manager) deleteStoredArtifact(artifact domain.Artifact) {
	if m.config.ArtifactStore == nil || artifact.StorageKey == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.config.StoreCleanupLimit)
	defer cancel()
	if err := m.config.ArtifactStore.Delete(ctx, artifact.StorageKey); err != nil {
		slog.Error("delete artifact from object storage failed", "storage_key", artifact.StorageKey, "error", err)
	}
}

type jobReporter struct {
	job *domain.Job
}

func (r jobReporter) SetTotalPages(total int) {
	r.job.SetTotalPages(total)
}

func (r jobReporter) Progress(stage domain.Stage, processedPages int) {
	r.job.ReportProgress(stage, processedPages)
}

func writeLimitedFile(path string, input io.Reader, limit int64) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create source file: %w", err)
	}
	written, copyErr := io.Copy(file, io.LimitReader(input, limit+1))
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("write source file: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close source file: %w", closeErr)
	}
	if written > limit {
		return ErrUploadTooLarge
	}
	return nil
}

func randomID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func safeSourceName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == "" {
		return "document.pdf"
	}
	return name
}
