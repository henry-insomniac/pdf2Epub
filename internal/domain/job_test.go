package domain

import (
	"errors"
	"testing"
)

func TestJobLifecycle(t *testing.T) {
	job := NewJob("job-1", "book.pdf", 12)

	assertStatus(t, job, JobQueued)
	if err := job.Start(); err != nil {
		t.Fatalf("start job: %v", err)
	}
	job.ReportProgress(StageExtracting, 5)
	job.AddWarning(Warning{Code: "content.table_as_image", Message: "第 8 页表格已转为图片"})
	if err := job.Succeed(Artifact{Name: "book.epub", Path: "/tmp/book.epub", Size: 42}); err != nil {
		t.Fatalf("succeed job: %v", err)
	}

	snapshot := job.Snapshot()
	if snapshot.Status != JobSucceeded {
		t.Fatalf("status = %q, want %q", snapshot.Status, JobSucceeded)
	}
	if snapshot.Stage != StageCompleted {
		t.Fatalf("stage = %q, want %q", snapshot.Stage, StageCompleted)
	}
	if snapshot.ProcessedPages != 5 || snapshot.TotalPages != 12 {
		t.Fatalf("progress = %d/%d, want 5/12", snapshot.ProcessedPages, snapshot.TotalPages)
	}
	if len(snapshot.Warnings) != 1 {
		t.Fatalf("warnings = %d, want 1", len(snapshot.Warnings))
	}
	if snapshot.Artifact == nil || snapshot.Artifact.Name != "book.epub" {
		t.Fatalf("artifact = %#v", snapshot.Artifact)
	}
	if snapshot.FinishedAt == nil {
		t.Fatal("finished_at is nil")
	}
	if err := job.Fail(Failure{Code: "late_failure", Message: "must not replace success"}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("late fail error = %v, want ErrInvalidTransition", err)
	}
}

func TestQueuedJobCanBeCanceled(t *testing.T) {
	job := NewJob("job-1", "book.pdf", 12)

	if err := job.Cancel(); err != nil {
		t.Fatalf("cancel job: %v", err)
	}

	snapshot := job.Snapshot()
	if snapshot.Status != JobCanceled {
		t.Fatalf("status = %q, want %q", snapshot.Status, JobCanceled)
	}
	if snapshot.FinishedAt == nil {
		t.Fatal("finished_at is nil")
	}
}

func TestFailedJobHasNoArtifact(t *testing.T) {
	job := NewJob("job-1", "book.pdf", 12)
	if err := job.Start(); err != nil {
		t.Fatalf("start job: %v", err)
	}
	if err := job.Fail(Failure{Code: "input.scanned_pdf", Message: "PDF 没有可提取文本"}); err != nil {
		t.Fatalf("fail job: %v", err)
	}

	snapshot := job.Snapshot()
	if snapshot.Status != JobFailed {
		t.Fatalf("status = %q, want %q", snapshot.Status, JobFailed)
	}
	if snapshot.Failure == nil || snapshot.Failure.Code != "input.scanned_pdf" {
		t.Fatalf("failure = %#v", snapshot.Failure)
	}
	if snapshot.Artifact != nil {
		t.Fatalf("artifact = %#v, want nil", snapshot.Artifact)
	}
}

func assertStatus(t *testing.T, job *Job, want JobStatus) {
	t.Helper()
	if got := job.Snapshot().Status; got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
}
