package database

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/miloszkolber/legacy-image-lab/internal/jobs"
)

func TestRepositoryJobLifecycle(t *testing.T) {
	repo, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	now := time.Now().UTC().Truncate(time.Microsecond)
	job := jobs.Job{ID: "job-1", Engine: "test-pattern", Status: jobs.Queued, Parameters: json.RawMessage(`{"iterations":5}`), Seed: 42, CreatedAt: now, EngineVersion: "0.1.0", RuntimeDevice: "cpu", RuntimePrecision: "fp32"}
	if err := repo.Create(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	job.Status = jobs.Running
	job.ProgressStep, job.ProgressTotal = 2, 5
	if err := repo.Save(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != jobs.Running || got.ProgressStep != 2 || got.Seed != 42 {
		t.Fatalf("unexpected persisted job: %+v", got)
	}
	if _, err := repo.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing job error = %v, want ErrNotFound", err)
	}
}
