package orchestrator

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/miloszkolber/legacy-image-lab/internal/config"
	"github.com/miloszkolber/legacy-image-lab/internal/database"
	"github.com/miloszkolber/legacy-image-lab/internal/events"
	"github.com/miloszkolber/legacy-image-lab/internal/jobs"
)

type Orchestrator struct {
	repo   *database.Repository
	broker *events.Broker
	cfg    config.Config
	logger *slog.Logger
	queue  chan string
	stop   chan struct{}
	done   chan struct{}

	mu              sync.Mutex
	active          map[string]*exec.Cmd
	cancelRequested map[string]bool
	closed          bool
}

func New(repo *database.Repository, broker *events.Broker, cfg config.Config, logger *slog.Logger) *Orchestrator {
	return &Orchestrator{repo: repo, broker: broker, cfg: cfg, logger: logger, queue: make(chan string, 4096), stop: make(chan struct{}), done: make(chan struct{}), active: make(map[string]*exec.Cmd), cancelRequested: make(map[string]bool)}
}

func (o *Orchestrator) Start() { go o.loop() }

func (o *Orchestrator) Stop() {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return
	}
	o.closed = true
	close(o.stop)
	commands := make([]*exec.Cmd, 0, len(o.active))
	for _, command := range o.active {
		commands = append(commands, command)
	}
	o.mu.Unlock()
	for _, command := range commands {
		_ = command.Process.Signal(syscall.SIGTERM)
		forceKillAfter(command, 3*time.Second)
	}
	<-o.done
}

func (o *Orchestrator) QueueLength() int { return len(o.queue) }

func (o *Orchestrator) Enqueue(id string) error {
	o.mu.Lock()
	closed := o.closed
	o.mu.Unlock()
	if closed {
		return errors.New("orchestrator is stopped")
	}
	select {
	case o.queue <- id:
		return nil
	default:
		return errors.New("job queue is full")
	}
}

func (o *Orchestrator) Recover(ctx context.Context) error {
	all, err := o.repo.ListRecoverable(ctx)
	if err != nil {
		return err
	}
	for _, job := range all {
		switch job.Status {
		case jobs.Queued:
			if err := o.Enqueue(job.ID); err != nil {
				return err
			}
		case jobs.Preparing, jobs.LoadingModel, jobs.Running, jobs.Saving:
			now := time.Now().UTC()
			job.Status = jobs.Failed
			job.ErrorCode = "ENGINE_CRASHED"
			job.ErrorMessage = "The application stopped while this job was running"
			job.CompletedAt = &now
			if err := o.repo.Save(ctx, job); err != nil {
				return err
			}
		}
	}
	return nil
}

func (o *Orchestrator) Cancel(ctx context.Context, id string) error {
	job, err := o.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if job.Status == jobs.Completed || job.Status == jobs.Failed || job.Status == jobs.Cancelled {
		return errors.New("job has already finished")
	}

	o.mu.Lock()
	o.cancelRequested[id] = true
	command := o.active[id]
	o.mu.Unlock()
	if command != nil && command.Process != nil {
		if err := command.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("signal worker: %w", err)
		}
		forceKillAfter(command, 3*time.Second)
		return nil
	}

	now := time.Now().UTC()
	job.Status = jobs.Cancelled
	job.ErrorCode = "CANCELLED"
	job.ErrorMessage = "Cancelled before execution"
	job.CompletedAt = &now
	if err := o.repo.Save(ctx, job); err != nil {
		return err
	}
	o.publish(job.ID, "cancelled", job)
	return nil
}

func (o *Orchestrator) loop() {
	defer close(o.done)
	for {
		select {
		case <-o.stop:
			return
		case id := <-o.queue:
			select {
			case <-o.stop:
				return
			default:
			}
			o.execute(id)
		}
	}
}

type workerEvent struct {
	Event   string `json:"event"`
	Model   string `json:"model,omitempty"`
	Step    int    `json:"step,omitempty"`
	Total   int    `json:"total,omitempty"`
	Path    string `json:"path,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Device  string `json:"device,omitempty"`
}

func (o *Orchestrator) execute(id string) {
	defer o.clearCancellation(id)
	ctx := context.Background()
	job, err := o.repo.Get(ctx, id)
	if err != nil || job.Status != jobs.Queued {
		return
	}
	now := time.Now().UTC()
	job.Status = jobs.Preparing
	job.StartedAt = &now
	if err := o.repo.Save(ctx, job); err != nil {
		o.logger.Error("prepare job", "job_id", id, "error", err)
		return
	}
	o.publish(id, "preparing", job)

	jobDir := filepath.Join(o.cfg.JobRoot(), id)
	if err := os.MkdirAll(filepath.Join(jobDir, "previews"), 0o750); err != nil {
		o.fail(&job, "ARTIFACT_WRITE_FAILED", err.Error())
		return
	}
	spec := map[string]any{
		"id": job.ID, "engine": job.Engine, "parameters": job.Parameters, "seed": job.Seed,
		"runtime": map[string]any{"device": job.RuntimeDevice, "precision": job.RuntimePrecision},
		"preview": map[string]any{"enabled": o.cfg.Previews.Enabled, "every_steps": o.cfg.Previews.EverySteps},
	}
	jobPath := filepath.Join(jobDir, "job.json")
	if err := atomicJSON(jobPath, spec); err != nil {
		o.fail(&job, "ARTIFACT_WRITE_FAILED", err.Error())
		return
	}
	if o.isCancelled(id) {
		o.markCancelled(&job, "Cancelled before worker startup")
		return
	}

	stdoutLog, err := os.OpenFile(filepath.Join(jobDir, "stdout.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		o.fail(&job, "ARTIFACT_WRITE_FAILED", err.Error())
		return
	}
	defer stdoutLog.Close()
	stderrLog, err := os.OpenFile(filepath.Join(jobDir, "stderr.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		o.fail(&job, "ARTIFACT_WRITE_FAILED", err.Error())
		return
	}
	defer stderrLog.Close()

	command := exec.Command(o.cfg.Runtime.PythonExecutable, "-m", "legacy_lab.runner", "--engine", job.Engine, "--job", jobPath)
	command.Dir = jobDir
	command.Env = append(os.Environ(), "PYTHONPATH="+o.cfg.Runtime.PythonPath)
	stdout, err := command.StdoutPipe()
	if err != nil {
		o.fail(&job, "ENGINE_CRASHED", err.Error())
		return
	}
	command.Stderr = stderrLog
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		o.markCancelled(&job, "Application is shutting down")
		return
	}
	if err := command.Start(); err != nil {
		o.mu.Unlock()
		o.fail(&job, "ENGINE_CRASHED", err.Error())
		return
	}
	o.active[id] = command
	cancelAfterStart := o.cancelRequested[id]
	o.mu.Unlock()
	if cancelAfterStart {
		_ = command.Process.Signal(syscall.SIGTERM)
		forceKillAfter(command, 3*time.Second)
	}

	scanner := bufio.NewScanner(io.TeeReader(stdout, stdoutLog))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	workerFailed := false
	for scanner.Scan() {
		var event workerEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			o.logger.Warn("invalid worker event", "job_id", id, "error", err)
			continue
		}
		if o.applyEvent(&job, event) {
			workerFailed = true
		}
	}
	if err := scanner.Err(); err != nil {
		o.logger.Error("read worker events", "job_id", id, "error", err)
	}
	waitErr := command.Wait()
	o.mu.Lock()
	delete(o.active, id)
	o.mu.Unlock()

	if job.Status == jobs.Failed || job.Status == jobs.Cancelled {
		return
	}
	if o.isCancelled(id) {
		o.markCancelled(&job, "Generation cancelled")
		return
	}
	if waitErr != nil {
		code := "ENGINE_CRASHED"
		message := "Worker exited before completing"
		if exit, ok := waitErr.(*exec.ExitError); ok && (exit.ExitCode() == 143 || exit.ExitCode() == -1) {
			o.markCancelled(&job, "Generation cancelled")
			return
		}
		if !workerFailed {
			o.fail(&job, code, message)
		}
		return
	}
	if job.Status == jobs.Saving {
		job.Status = jobs.Completed
		if err := o.finish(&job, "", ""); err != nil {
			o.logger.Error("persist completed job", "job_id", job.ID, "error", err)
		}
		return
	}
	o.fail(&job, "ENGINE_CRASHED", "Worker exited without a completed event")
}

func (o *Orchestrator) applyEvent(job *jobs.Job, event workerEvent) bool {
	if o.isCancelled(job.ID) && event.Event != "error" {
		return false
	}
	if job.Status == jobs.Failed || job.Status == jobs.Cancelled {
		return true
	}
	if job.Status == jobs.Saving {
		o.fail(job, "INVALID_WORKER_EVENT", "Worker emitted data after completion")
		return true
	}
	switch event.Event {
	case "started":
		if job.Status != jobs.Preparing && job.Status != jobs.LoadingModel {
			o.fail(job, "INVALID_WORKER_EVENT", "Worker started from an invalid state")
			return true
		}
		job.Status = jobs.Running
		if event.Device == "xpu" || event.Device == "cpu" {
			job.RuntimeDevice = event.Device
		}
	case "model-loading":
		if job.Status != jobs.Preparing {
			o.fail(job, "INVALID_WORKER_EVENT", "Worker loaded a model from an invalid state")
			return true
		}
		job.Status = jobs.LoadingModel
	case "progress":
		if job.Status != jobs.Running || event.Total < 1 || event.Step < 0 || event.Step > event.Total || (job.ProgressTotal == event.Total && event.Step < job.ProgressStep) {
			o.fail(job, "INVALID_WORKER_EVENT", "Worker returned invalid progress")
			return true
		}
		job.Status, job.ProgressStep, job.ProgressTotal = jobs.Running, event.Step, event.Total
	case "preview":
		if job.Status != jobs.Running {
			o.fail(job, "INVALID_WORKER_EVENT", "Worker previewed from an invalid state")
			return true
		}
		if err := o.validateArtifact(job.ID, event.Path); err != nil {
			o.fail(job, "INVALID_WORKER_EVENT", "Worker returned an invalid preview artifact")
			return true
		}
		job.PreviewPath = event.Path
	case "completed":
		if job.Status != jobs.Running {
			o.fail(job, "INVALID_WORKER_EVENT", "Worker completed from an invalid state")
			return true
		}
		if err := o.validateArtifact(job.ID, event.Path); err != nil {
			o.fail(job, "INVALID_WORKER_EVENT", "Worker returned an invalid artifact path")
			return true
		}
		job.Status, job.FinalPath = jobs.Saving, event.Path
		if err := o.repo.Save(context.Background(), *job); err != nil {
			o.logger.Error("save completion event", "job_id", job.ID, "error", err)
			o.fail(job, "PERSISTENCE_FAILED", "Could not persist worker completion")
			return true
		}
		o.publish(job.ID, "saving", *job)
		return false
	case "error":
		if event.Code == "" {
			event.Code = "ENGINE_CRASHED"
		}
		if event.Code == "CANCELLED" {
			job.Status = jobs.Cancelled
			if err := o.finish(job, event.Code, event.Message); err != nil {
				o.logger.Error("persist cancelled job", "job_id", job.ID, "error", err)
			}
			return true
		}
		o.fail(job, event.Code, event.Message)
		return true
	default:
		return false
	}
	if err := o.repo.Save(context.Background(), *job); err != nil {
		o.logger.Error("save worker event", "job_id", job.ID, "error", err)
	}
	o.publish(job.ID, event.Event, event)
	return false
}

func (o *Orchestrator) fail(job *jobs.Job, code, message string) {
	job.Status = jobs.Failed
	if err := o.finish(job, code, message); err != nil {
		o.logger.Error("persist failed job", "job_id", job.ID, "error", err)
	}
}

func (o *Orchestrator) finish(job *jobs.Job, code, message string) error {
	now := time.Now().UTC()
	job.ErrorCode, job.ErrorMessage, job.CompletedAt = code, message, &now
	if err := o.saveTerminal(*job); err != nil {
		return err
	}
	o.publish(job.ID, string(job.Status), *job)
	if job.Status == jobs.Completed {
		metadata := map[string]any{"job": job, "application": "Legacy Image Lab", "completed_at": now}
		if err := atomicJSON(filepath.Join(o.cfg.JobRoot(), job.ID, "metadata.json"), metadata); err != nil {
			o.logger.Error("write metadata", "job_id", job.ID, "error", err)
		}
	}
	return nil
}

func (o *Orchestrator) publish(jobID, eventName string, payload any) {
	data, _ := json.Marshal(payload)
	o.broker.Publish(events.Event{JobID: jobID, Event: eventName, Data: data})
}

func safeRelative(path string) bool {
	clean := filepath.Clean(path)
	return path != "" && !filepath.IsAbs(path) && clean == path && clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func (o *Orchestrator) validateArtifact(jobID, relative string) error {
	if !safeRelative(relative) {
		return errors.New("artifact path is not relative")
	}
	root := filepath.Join(o.cfg.JobRoot(), jobID)
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, relative))
	if err != nil {
		return err
	}
	if resolved != resolvedRoot && !strings.HasPrefix(resolved, resolvedRoot+string(filepath.Separator)) {
		return errors.New("artifact escapes job root")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("artifact is not a regular file")
	}
	return nil
}

func (o *Orchestrator) isCancelled(id string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.cancelRequested[id]
}

func (o *Orchestrator) clearCancellation(id string) {
	o.mu.Lock()
	delete(o.cancelRequested, id)
	o.mu.Unlock()
}

func (o *Orchestrator) markCancelled(job *jobs.Job, message string) {
	job.Status = jobs.Cancelled
	if err := o.finish(job, "CANCELLED", message); err != nil {
		o.logger.Error("persist cancelled job", "job_id", job.ID, "error", err)
	}
}

func (o *Orchestrator) saveTerminal(job jobs.Job) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = o.repo.Save(context.Background(), job); err == nil {
			return nil
		}
		if attempt < 2 {
			time.Sleep(50 * time.Millisecond)
		}
	}
	return fmt.Errorf("save terminal job state: %w", err)
}

func forceKillAfter(command *exec.Cmd, delay time.Duration) {
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		<-timer.C
		if command.Process != nil {
			_ = command.Process.Kill()
		}
	}()
}

func atomicJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := os.WriteFile(temporary, append(data, '\n'), 0o640); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}
