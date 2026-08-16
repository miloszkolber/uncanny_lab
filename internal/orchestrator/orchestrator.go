package orchestrator

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/miloszkolber/uncanny-lab/internal/config"
	"github.com/miloszkolber/uncanny-lab/internal/database"
	"github.com/miloszkolber/uncanny-lab/internal/events"
	"github.com/miloszkolber/uncanny-lab/internal/jobs"
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
	processDone     map[string]chan struct{}
	cancelRequested map[string]bool
	completionSaved map[string]bool
	completing      map[string]chan struct{}
	closed          bool
}

func New(repo *database.Repository, broker *events.Broker, cfg config.Config, logger *slog.Logger) *Orchestrator {
	return &Orchestrator{repo: repo, broker: broker, cfg: cfg, logger: logger, queue: make(chan string, 4096), stop: make(chan struct{}), done: make(chan struct{}), active: make(map[string]*exec.Cmd), processDone: make(map[string]chan struct{}), cancelRequested: make(map[string]bool), completionSaved: make(map[string]bool), completing: make(map[string]chan struct{})}
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
	type activeProcess struct {
		command *exec.Cmd
		done    <-chan struct{}
	}
	commands := make([]activeProcess, 0, len(o.active))
	for id, command := range o.active {
		commands = append(commands, activeProcess{command: command, done: o.processDone[id]})
	}
	o.mu.Unlock()
	for _, process := range commands {
		signalProcessGroup(process.command, syscall.SIGTERM)
		forceKillAfter(process.command, 3*time.Second, process.done)
	}
	select {
	case <-o.done:
	case <-time.After(4 * time.Second):
		o.logger.Warn("orchestrator shutdown timed out waiting for workers")
	}
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
			job.Status = jobs.Failed
			if err := o.finish(&job, "ENGINE_CRASHED", "The application stopped while this job was running"); err != nil {
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
	if o.completionSaved[id] {
		o.mu.Unlock()
		return errors.New("job has already finished")
	}
	if done := o.completing[id]; done != nil {
		o.mu.Unlock()
		select {
		case <-done:
			return o.Cancel(ctx, id)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	// The first read may have observed Saving immediately before complete
	// committed Completed and cleared its in-memory marker. Recheck while
	// excluding complete from starting before claiming cancellation.
	job, err = o.repo.Get(ctx, id)
	if err != nil {
		o.mu.Unlock()
		return err
	}
	if job.Status == jobs.Completed || job.Status == jobs.Failed || job.Status == jobs.Cancelled {
		o.mu.Unlock()
		return errors.New("job has already finished")
	}
	o.cancelRequested[id] = true
	command := o.active[id]
	done := o.processDone[id]
	o.mu.Unlock()
	if command != nil && command.Process != nil {
		if err := signalProcessGroup(command, syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("signal worker: %w", err)
		}
		forceKillAfter(command, 3*time.Second, done)
		return nil
	}

	now := time.Now().UTC()
	job.Status = jobs.Cancelled
	job.ErrorCode = "CANCELLED"
	job.ErrorMessage = "Cancelled before execution"
	job.CompletedAt = &now
	if err := o.writeTerminalMetadata(&job); err != nil {
		return err
	}
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
	materialized, err := MaterializeImageParameters(o.cfg, jobDir, job.Parameters)
	if err != nil {
		o.fail(&job, "INVALID_IMAGE_INPUT", err.Error())
		return
	}
	spec["parameters"] = materialized
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

	command := exec.Command(o.cfg.Runtime.PythonExecutable, "-m", "uncanny_lab.runner", "--engine", job.Engine, "--job", jobPath)
	command.Dir = jobDir
	command.Env = append(os.Environ(), "PYTHONPATH="+o.cfg.Runtime.PythonPath, "UNCANNY_DATA_ROOT="+o.cfg.Paths.Data, "UNCANNY_MODELS_ROOT="+o.cfg.Paths.Models)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
	processDone := make(chan struct{})
	o.active[id] = command
	o.processDone[id] = processDone
	cancelAfterStart := o.cancelRequested[id]
	o.mu.Unlock()
	if cancelAfterStart {
		_ = signalProcessGroup(command, syscall.SIGTERM)
		forceKillAfter(command, 3*time.Second, processDone)
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
	if done := o.processDone[id]; done != nil {
		close(done)
		delete(o.processDone, id)
	}
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
		if shouldFailWorkerExit(waitErr, workerFailed) {
			o.fail(&job, code, message)
		}
		return
	}
	if job.Status == jobs.Saving {
		if err := o.complete(&job); err != nil {
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

func (o *Orchestrator) MarkFailed(job *jobs.Job, code, message string) error {
	job.Status = jobs.Failed
	return o.finish(job, code, message)
}

func (o *Orchestrator) finish(job *jobs.Job, code, message string) error {
	now := time.Now().UTC()
	job.ErrorCode, job.ErrorMessage, job.CompletedAt = code, message, &now
	if err := o.writeTerminalMetadata(job); err != nil {
		return err
	}
	if err := o.saveTerminal(*job); err != nil {
		return err
	}
	o.publish(job.ID, string(job.Status), *job)
	return nil
}

func (o *Orchestrator) writeTerminalMetadata(job *jobs.Job) error {
	directory := filepath.Join(o.cfg.JobRoot(), job.ID)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("create terminal artifact directory: %w", err)
	}
	metadata := map[string]any{"job": job, "application": "Uncanny Lab", "terminal_at": job.CompletedAt}
	if job.Status == jobs.Completed {
		metadata["completed_at"] = job.CompletedAt
		metadata["file_hashes"] = o.jobFileHashes(job.ID)
	}
	path := filepath.Join(directory, "metadata.json")
	if err := atomicJSON(path, metadata); err != nil {
		return fmt.Errorf("write terminal metadata: %w", err)
	}
	return nil
}

func (o *Orchestrator) jobFileHashes(jobID string) map[string]string {
	jobDir := filepath.Join(o.cfg.JobRoot(), jobID)
	data, err := os.ReadFile(filepath.Join(jobDir, "job.json"))
	if err != nil {
		return nil
	}
	var spec struct {
		Parameters map[string]any `json:"parameters"`
	}
	if json.Unmarshal(data, &spec) != nil {
		return nil
	}
	result := make(map[string]string)
	for key, value := range spec.Parameters {
		// Only per-job input copies are hashed. Checkpoint and generator paths
		// are immutable catalog artifacts whose integrity is already pinned by
		// the bundle provenance report, so re-hashing gigabytes per job would
		// only stall completion.
		path, ok := value.(string)
		if !ok || !strings.HasSuffix(key, "_image") {
			continue
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || !strings.HasPrefix(resolved, o.cfg.Paths.Data+string(filepath.Separator)) {
			continue
		}
		file, err := os.Open(resolved)
		if err != nil {
			continue
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr == nil && closeErr == nil {
			result[key] = hex.EncodeToString(hash.Sum(nil))
		}
	}
	return result
}

var inputTokenID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var jobTokenID = regexp.MustCompile(`^[0-9a-f]+-[0-9a-f]{12}$`)

// MaterializeImageParameters replaces accepted image tokens in the worker spec with
// private copies. The database keeps the original tokens for reproducibility.
func MaterializeImageParameters(cfg config.Config, jobDir string, raw json.RawMessage) (json.RawMessage, error) {
	var parameters map[string]any
	if err := json.Unmarshal(raw, &parameters); err != nil {
		return nil, errors.New("parameters are invalid")
	}
	for _, key := range []string{"source_image", "style_image", "init_image"} {
		value, exists := parameters[key]
		if !exists || value == nil {
			continue
		}
		token, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%s must be an image token", key)
		}
		source, err := resolveImageToken(cfg, token)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		destination := filepath.Join(jobDir, "inputs", key+".png")
		if err := copyRegularFile(source, destination); err != nil {
			return nil, fmt.Errorf("copy %s: %w", key, err)
		}
		parameters[key] = destination
	}
	return json.Marshal(parameters)
}

func resolveImageToken(cfg config.Config, token string) (string, error) {
	var root, relative string
	if strings.HasPrefix(token, "inputs/") {
		root, relative = cfg.Paths.Inputs, strings.TrimPrefix(token, "inputs/")
		if !inputTokenID.MatchString(strings.TrimSuffix(relative, ".png")) || filepath.Ext(relative) != ".png" {
			return "", errors.New("invalid input token")
		}
	} else if strings.HasPrefix(token, "workspace/jobs/") {
		root, relative = cfg.JobRoot(), strings.TrimPrefix(token, "workspace/jobs/")
		parts := strings.Split(filepath.ToSlash(relative), "/")
		validArtifact := len(parts) == 2 && parts[1] == "final.png" || len(parts) == 3 && parts[1] == "previews" && previewToken(parts[2])
		if !jobTokenID.MatchString(parts[0]) || !validArtifact {
			return "", errors.New("invalid workspace token")
		}
	} else {
		return "", errors.New("unrecognized image token")
	}
	return resolvedContainedFile(root, relative)
}

func previewToken(value string) bool {
	return strings.HasSuffix(value, ".png") && strings.TrimSuffix(value, ".png") != "" && filepath.Base(value) == value
}

func resolvedContainedFile(root, relative string) (string, error) {
	if filepath.IsAbs(relative) || filepath.Clean(relative) != relative || strings.HasPrefix(relative, "..") {
		return "", errors.New("invalid path")
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(resolvedRoot, relative))
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(resolved, resolvedRoot+string(filepath.Separator)) {
		return "", errors.New("path escapes allowed root")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("image is not a regular file")
	}
	return resolved, nil
}

func copyRegularFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return err
	}
	temporary := destination + ".tmp"
	out, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = out.Close()
		_ = os.Remove(temporary)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(temporary)
		return closeErr
	}
	return os.Rename(temporary, destination)
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
	delete(o.completionSaved, id)
	o.mu.Unlock()
}

// complete commits a completed state while excluding a concurrent cancellation.
// Cancel waits for an in-progress completion and checks completionSaved before
// acting on a stale Saving state.
func (o *Orchestrator) complete(job *jobs.Job) error {
	o.mu.Lock()
	if o.cancelRequested[job.ID] {
		o.mu.Unlock()
		o.markCancelled(job, "Generation cancelled")
		return nil
	}
	done := make(chan struct{})
	o.completing[job.ID] = done
	o.mu.Unlock()
	job.Status = jobs.Completed
	err := o.finish(job, "", "")
	if err == nil {
		o.prunePreviews(job.ID)
	}
	o.mu.Lock()
	delete(o.completing, job.ID)
	if err == nil {
		o.completionSaved[job.ID] = true
	}
	close(done)
	o.mu.Unlock()
	return err
}

func (o *Orchestrator) markCancelled(job *jobs.Job, message string) {
	job.Status = jobs.Cancelled
	if err := o.finish(job, "CANCELLED", message); err != nil {
		o.logger.Error("persist cancelled job", "job_id", job.ID, "error", err)
	}
}

// prunePreviews bounds per-job disk usage by keeping only the most recent
// max_frames previews of a completed job. The timeline reflects the disk, so
// older frames disappear consistently everywhere.
func (o *Orchestrator) prunePreviews(jobID string) {
	maxFrames := o.cfg.Previews.MaxFrames
	if maxFrames < 1 {
		return
	}
	directory := filepath.Join(o.cfg.JobRoot(), jobID, "previews")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	var frames []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".png") {
			frames = append(frames, entry.Name())
		}
	}
	sort.Strings(frames)
	if len(frames) <= maxFrames {
		return
	}
	for _, name := range frames[:len(frames)-maxFrames] {
		if removeErr := os.Remove(filepath.Join(directory, name)); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			o.logger.Debug("prune preview", "job_id", jobID, "file", name, "error", removeErr)
		}
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

func forceKillAfter(command *exec.Cmd, delay time.Duration, done <-chan struct{}) {
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-done:
			return
		case <-timer.C:
		}
		_ = signalProcessGroup(command, syscall.SIGKILL)
	}()
}

func signalProcessGroup(command *exec.Cmd, signal syscall.Signal) error {
	if command == nil || command.Process == nil {
		return os.ErrProcessDone
	}
	return syscall.Kill(-command.Process.Pid, signal)
}

func shouldFailWorkerExit(waitErr error, workerFailed bool) bool {
	return waitErr != nil && !workerFailed
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
