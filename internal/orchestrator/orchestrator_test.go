package orchestrator

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/miloszkolber/uncanny-lab/internal/config"
	"github.com/miloszkolber/uncanny-lab/internal/database"
	"github.com/miloszkolber/uncanny-lab/internal/events"
	"github.com/miloszkolber/uncanny-lab/internal/jobs"
)

func TestErrorEventCannotBeOverwrittenByCompletion(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		Runtime:  config.RuntimeConfig{Device: "cpu", DefaultPrecision: "fp32", PythonExecutable: "python3", PythonPath: root},
		Paths:    config.PathsConfig{Models: filepath.Join(root, "models"), Inputs: filepath.Join(root, "inputs"), Workspace: filepath.Join(root, "workspace"), Manifests: filepath.Join(root, "manifests")},
		Previews: config.PreviewConfig{Enabled: true, EverySteps: 1},
	}
	if err := cfg.EnsureDirectories(); err != nil {
		t.Fatal(err)
	}
	repo, err := database.Open(cfg.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	job := jobs.Job{ID: "job-1", Engine: "test-pattern", Status: jobs.Running, Parameters: json.RawMessage(`{}`), CreatedAt: time.Now().UTC(), EngineVersion: "0.1.0", RuntimeDevice: "cpu", RuntimePrecision: "fp32"}
	if err := repo.Create(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	orchestrator := New(repo, events.NewBroker(), cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if terminal := orchestrator.applyEvent(&job, workerEvent{Event: "error", Code: "ENGINE_CRASHED", Message: "failed"}); !terminal {
		t.Fatal("error event was not terminal")
	}
	if terminal := orchestrator.applyEvent(&job, workerEvent{Event: "completed", Path: "final.png"}); !terminal {
		t.Fatal("event after failure was not rejected")
	}
	if job.Status != jobs.Failed || job.FinalPath != "" {
		t.Fatalf("terminal state was overwritten: %+v", job)
	}
}

func TestJobFileHashesIncludesOnlyPerJobInputFiles(t *testing.T) {
	root := t.TempDir()
	jobID := "123-0123456789ab"
	jobDir := filepath.Join(root, "workspace", "jobs", jobID)
	input := filepath.Join(jobDir, "inputs", "source_image.png")
	model := filepath.Join(root, "models", "model.pt")
	if err := os.MkdirAll(filepath.Dir(input), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(model), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, []byte("image"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(model, []byte("model"), 0o640); err != nil {
		t.Fatal(err)
	}
	spec, err := json.Marshal(map[string]any{"parameters": map[string]any{"source_image": input, "classifier_path": model, "prompt": "/etc/passwd"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "job.json"), spec, 0o640); err != nil {
		t.Fatal(err)
	}
	orchestrator := &Orchestrator{cfg: config.Config{Paths: config.PathsConfig{Data: root, Workspace: filepath.Join(root, "workspace")}}}
	hashes := orchestrator.jobFileHashes(jobID)
	if len(hashes) != 1 || hashes["source_image"] == "" {
		t.Fatalf("unexpected file hashes: %#v", hashes)
	}
}

func TestQueuedCancellationWritesRecoverableMetadata(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{Paths: config.PathsConfig{Data: root, Models: filepath.Join(root, "models"), Inputs: filepath.Join(root, "inputs"), Workspace: filepath.Join(root, "workspace")}}
	if err := cfg.EnsureDirectories(); err != nil {
		t.Fatal(err)
	}
	repo, err := database.Open(cfg.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	job := jobs.Job{ID: "123-0123456789ab", Engine: "deep-image-prior", Status: jobs.Queued, Parameters: json.RawMessage(`{}`), CreatedAt: time.Now().UTC(), EngineVersion: "1.0.0", RuntimeDevice: "cpu", RuntimePrecision: "fp32"}
	if err := repo.Create(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	runner := New(repo, events.NewBroker(), cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := runner.Cancel(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(cfg.JobRoot(), job.ID, "metadata.json")
	if _, err := os.Stat(metadata); err != nil {
		t.Fatalf("terminal metadata is missing: %v", err)
	}
	persisted, err := repo.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != jobs.Cancelled || persisted.ErrorCode != "CANCELLED" {
		t.Fatalf("cancelled job = %+v", persisted)
	}
}

func TestCompleteHonorsCancellationRequestedWhileSaving(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{Paths: config.PathsConfig{Data: root, Models: filepath.Join(root, "models"), Inputs: filepath.Join(root, "inputs"), Workspace: filepath.Join(root, "workspace")}}
	if err := cfg.EnsureDirectories(); err != nil {
		t.Fatal(err)
	}
	repo, err := database.Open(cfg.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	job := jobs.Job{ID: "123-0123456789ab", Engine: "deep-image-prior", Status: jobs.Saving, Parameters: json.RawMessage(`{}`), CreatedAt: time.Now().UTC(), EngineVersion: "1.0.0", RuntimeDevice: "cpu", RuntimePrecision: "fp32"}
	if err := repo.Create(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	runner := New(repo, events.NewBroker(), cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	runner.mu.Lock()
	runner.cancelRequested[job.ID] = true
	runner.mu.Unlock()
	if err := runner.complete(&job); err != nil {
		t.Fatal(err)
	}
	persisted, err := repo.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != jobs.Cancelled || persisted.ErrorCode != "CANCELLED" {
		t.Fatalf("saving job was not cancelled: %+v", persisted)
	}
}

func TestCancellationDoesNotOverwriteCompletedSavingJob(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{Paths: config.PathsConfig{Data: root, Models: filepath.Join(root, "models"), Inputs: filepath.Join(root, "inputs"), Workspace: filepath.Join(root, "workspace")}}
	if err := cfg.EnsureDirectories(); err != nil {
		t.Fatal(err)
	}
	repo, err := database.Open(cfg.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	job := jobs.Job{ID: "123-0123456789ab", Engine: "deep-image-prior", Status: jobs.Saving, Parameters: json.RawMessage(`{}`), CreatedAt: time.Now().UTC(), EngineVersion: "1.0.0", RuntimeDevice: "cpu", RuntimePrecision: "fp32"}
	if err := repo.Create(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	runner := New(repo, events.NewBroker(), cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := runner.complete(&job); err != nil {
		t.Fatal(err)
	}
	if err := runner.Cancel(context.Background(), job.ID); err == nil {
		t.Fatal("cancellation succeeded after completion")
	}
	persisted, err := repo.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != jobs.Completed {
		t.Fatalf("completion was overwritten: %+v", persisted)
	}
}

func TestUnrequestedSignalExitIsWorkerFailure(t *testing.T) {
	command := exec.Command("sh", "-c", "kill -TERM $$")
	err := command.Run()
	if err == nil {
		t.Fatal("worker did not exit from SIGTERM")
	}
	if !shouldFailWorkerExit(err, false) {
		t.Fatalf("unrequested signal exit was not treated as a worker failure: %v", err)
	}
}

func TestPrunePreviewsKeepsOnlyNewestFrames(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{Paths: config.PathsConfig{Data: root, Workspace: filepath.Join(root, "workspace")}, Previews: config.PreviewConfig{MaxFrames: 2}}
	jobID := "123-0123456789ab"
	directory := filepath.Join(cfg.JobRoot(), jobID, "previews")
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"000001.png", "000002.png", "000003.png", "000004.png", "000005.png"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("frame"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	runner := New(nil, nil, cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	runner.prunePreviews(jobID)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	var remaining []string
	for _, entry := range entries {
		remaining = append(remaining, entry.Name())
	}
	if len(remaining) != 2 || remaining[0] != "000004.png" || remaining[1] != "000005.png" {
		t.Fatalf("remaining previews = %v, want the two newest", remaining)
	}
}
