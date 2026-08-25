package domain

import (
	"errors"
	"sync"
	"time"
)

var ErrInvalidTransition = errors.New("invalid job transition")

type JobStatus string

const (
	JobQueued     JobStatus = "queued"
	JobProcessing JobStatus = "processing"
	JobSucceeded  JobStatus = "succeeded"
	JobFailed     JobStatus = "failed"
	JobCanceled   JobStatus = "canceled"
)

type Stage string

const (
	StageWaiting    Stage = "waiting"
	StagePreflight  Stage = "preflight"
	StageExtracting Stage = "extracting"
	StageRebuilding Stage = "rebuilding"
	StagePackaging  Stage = "packaging"
	StageValidating Stage = "validating"
	StageCompleted  Stage = "completed"
	StageFailed     Stage = "failed"
	StageCanceled   Stage = "canceled"
)

type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Page    int    `json:"page,omitempty"`
}

type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Page    int    `json:"page,omitempty"`
}

type Artifact struct {
	Name string `json:"name"`
	Path string `json:"-"`
	Size int64  `json:"size"`
}

type Snapshot struct {
	ID             string     `json:"id"`
	SourceName     string     `json:"source_name"`
	Status         JobStatus  `json:"status"`
	Stage          Stage      `json:"stage"`
	ProcessedPages int        `json:"processed_pages,omitempty"`
	TotalPages     int        `json:"total_pages,omitempty"`
	Warnings       []Warning  `json:"warnings,omitempty"`
	Failure        *Failure   `json:"failure,omitempty"`
	Artifact       *Artifact  `json:"artifact,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

type Job struct {
	mu       sync.RWMutex
	snapshot Snapshot
}

func NewJob(id, sourceName string, totalPages int) *Job {
	return &Job{snapshot: Snapshot{
		ID:         id,
		SourceName: sourceName,
		Status:     JobQueued,
		Stage:      StageWaiting,
		TotalPages: totalPages,
		CreatedAt:  time.Now().UTC(),
	}}
}

func (j *Job) Snapshot() Snapshot {
	j.mu.RLock()
	defer j.mu.RUnlock()

	snapshot := j.snapshot
	snapshot.Warnings = append([]Warning(nil), j.snapshot.Warnings...)
	if j.snapshot.Failure != nil {
		failure := *j.snapshot.Failure
		snapshot.Failure = &failure
	}
	if j.snapshot.Artifact != nil {
		artifact := *j.snapshot.Artifact
		snapshot.Artifact = &artifact
	}
	return snapshot
}

func (j *Job) Start() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.snapshot.Status != JobQueued {
		return ErrInvalidTransition
	}
	now := time.Now().UTC()
	j.snapshot.Status = JobProcessing
	j.snapshot.Stage = StagePreflight
	j.snapshot.StartedAt = &now
	return nil
}

func (j *Job) ReportProgress(stage Stage, processedPages int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.snapshot.Status != JobProcessing {
		return
	}
	j.snapshot.Stage = stage
	if processedPages >= 0 {
		j.snapshot.ProcessedPages = processedPages
	}
}

func (j *Job) SetTotalPages(totalPages int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.snapshot.Status == JobProcessing && totalPages >= 0 {
		j.snapshot.TotalPages = totalPages
	}
}

func (j *Job) AddWarning(warning Warning) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.snapshot.Status == JobQueued || j.snapshot.Status == JobProcessing {
		j.snapshot.Warnings = append(j.snapshot.Warnings, warning)
	}
}

func (j *Job) Succeed(artifact Artifact) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.snapshot.Status != JobProcessing {
		return ErrInvalidTransition
	}
	now := time.Now().UTC()
	j.snapshot.Status = JobSucceeded
	j.snapshot.Stage = StageCompleted
	j.snapshot.Artifact = &artifact
	j.snapshot.FinishedAt = &now
	return nil
}

func (j *Job) Fail(failure Failure) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.snapshot.Status != JobProcessing && j.snapshot.Status != JobQueued {
		return ErrInvalidTransition
	}
	now := time.Now().UTC()
	j.snapshot.Status = JobFailed
	j.snapshot.Stage = StageFailed
	j.snapshot.Failure = &failure
	j.snapshot.Artifact = nil
	j.snapshot.FinishedAt = &now
	return nil
}

func (j *Job) Cancel() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.snapshot.Status != JobProcessing && j.snapshot.Status != JobQueued {
		return ErrInvalidTransition
	}
	now := time.Now().UTC()
	j.snapshot.Status = JobCanceled
	j.snapshot.Stage = StageCanceled
	j.snapshot.Artifact = nil
	j.snapshot.FinishedAt = &now
	return nil
}
