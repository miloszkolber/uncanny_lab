package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/miloszkolber/uncanny-lab/internal/jobs"
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

func TestOpenRestrictsDatabasePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.db")
	repo, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("database mode = %o, want 600", mode)
	}
}

func TestReconcileRestoresTerminalJobFromMetadata(t *testing.T) {
	root := t.TempDir()
	repo, err := Open(filepath.Join(root, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	statuses := []jobs.Status{jobs.Completed, jobs.Failed, jobs.Cancelled}
	for index, status := range statuses {
		job := jobs.Job{ID: fmt.Sprintf("12%d-0123456789ab", index), Engine: "deep-image-prior", Status: status, Parameters: json.RawMessage(`{"iterations":1}`), Seed: 42, CreatedAt: time.Now().UTC(), EngineVersion: "1.0.0", RuntimeDevice: "cpu", RuntimePrecision: "fp32"}
		if status == jobs.Completed {
			job.FinalPath = "final.png"
		}
		directory := filepath.Join(root, "jobs", job.ID)
		if err := os.MkdirAll(directory, 0o750); err != nil {
			t.Fatal(err)
		}
		metadata, err := json.Marshal(map[string]any{"application": "Uncanny Lab", "job": job})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "metadata.json"), metadata, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	stale := jobs.Job{ID: "122-0123456789ab", Engine: "deep-image-prior", Status: jobs.Queued, Parameters: json.RawMessage(`{}`), Seed: 42, CreatedAt: time.Now().UTC(), EngineVersion: "1.0.0", RuntimeDevice: "cpu", RuntimePrecision: "fp32"}
	if err := repo.Create(context.Background(), stale); err != nil {
		t.Fatal(err)
	}
	restored, err := repo.Reconcile(context.Background(), filepath.Join(root, "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	if restored != len(statuses)-1 {
		t.Fatalf("restored = %d, want %d", restored, len(statuses)-1)
	}
	for index, status := range statuses {
		got, err := repo.Get(context.Background(), fmt.Sprintf("12%d-0123456789ab", index))
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != status {
			t.Fatalf("restored status = %s, want %s", got.Status, status)
		}
	}
	if restored, err := repo.Reconcile(context.Background(), filepath.Join(root, "jobs")); err != nil || restored != 0 {
		t.Fatalf("second reconcile = %d, %v", restored, err)
	}
}

func TestReconcileBackfillsExistingTerminalJob(t *testing.T) {
	root := t.TempDir()
	repo, err := Open(filepath.Join(root, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	job := jobs.Job{ID: "123-0123456789ab", Engine: "deep-image-prior", Status: jobs.Cancelled, Parameters: json.RawMessage(`{}`), Seed: 42, CreatedAt: time.Now().UTC(), EngineVersion: "1.0.0", RuntimeDevice: "cpu", RuntimePrecision: "fp32"}
	if err := repo.Create(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	jobRoot := filepath.Join(root, "jobs")
	if _, err := repo.Reconcile(context.Background(), jobRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(jobRoot, job.ID, "metadata.json")); err != nil {
		t.Fatalf("backfilled metadata is missing: %v", err)
	}
	if err := repo.Delete(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if restored, err := repo.Reconcile(context.Background(), jobRoot); err != nil || restored != 1 {
		t.Fatalf("restore from backfill = %d, %v", restored, err)
	}
}
