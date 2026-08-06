package orchestrator

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/miloszkolber/legacy-image-lab/internal/config"
	"github.com/miloszkolber/legacy-image-lab/internal/database"
	"github.com/miloszkolber/legacy-image-lab/internal/events"
	"github.com/miloszkolber/legacy-image-lab/internal/jobs"
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
