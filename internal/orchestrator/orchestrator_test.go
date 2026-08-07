package orchestrator

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
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
		Paths:    config.PathsConfig{Models: filepath.Join(root, "models"), Inputs: filepath.Join(root, "inputs"), Outputs: filepath.Join(root, "outputs"), Workspace: filepath.Join(root, "workspace"), Manifests: filepath.Join(root, "manifests")},
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

func TestJobFileHashesIncludesOnlyApprovedParameterFiles(t *testing.T) {
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
	if len(hashes) != 2 || hashes["source_image"] == "" || hashes["classifier_path"] == "" {
		t.Fatalf("unexpected file hashes: %#v", hashes)
	}
}
