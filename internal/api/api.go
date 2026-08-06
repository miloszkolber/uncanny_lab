package api

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/miloszkolber/legacy-image-lab/internal/config"
	"github.com/miloszkolber/legacy-image-lab/internal/database"
	"github.com/miloszkolber/legacy-image-lab/internal/engines"
	"github.com/miloszkolber/legacy-image-lab/internal/events"
	"github.com/miloszkolber/legacy-image-lab/internal/jobs"
	"github.com/miloszkolber/legacy-image-lab/internal/orchestrator"
	webassets "github.com/miloszkolber/legacy-image-lab/web"
)

type API struct {
	repo         *database.Repository
	orchestrator *orchestrator.Orchestrator
	broker       *events.Broker
	cfg          config.Config
	version      string
	logger       *slog.Logger
	registry     *engines.Registry
	systemMu     sync.Mutex
	systemCache  map[string]any
	systemCached time.Time
}

var jobIDPattern = regexp.MustCompile(`^[0-9a-f]+-[0-9a-f]{12}$`)

func New(repo *database.Repository, runner *orchestrator.Orchestrator, broker *events.Broker, cfg config.Config, version string, logger *slog.Logger) (http.Handler, error) {
	static, err := webassets.Handler()
	if err != nil {
		return nil, err
	}
	registry, err := engines.Load(cfg.Paths.Manifests)
	if err != nil {
		return nil, err
	}
	a := &API{repo: repo, orchestrator: runner, broker: broker, cfg: cfg, version: version, logger: logger, registry: registry}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /api/engines", a.engines)
	mux.HandleFunc("GET /api/jobs", a.listJobs)
	mux.HandleFunc("POST /api/jobs", a.createJob)
	mux.HandleFunc("GET /api/jobs/{id}", a.getJob)
	mux.HandleFunc("POST /api/jobs/{id}/cancel", a.cancelJob)
	mux.HandleFunc("POST /api/jobs/{id}/duplicate", a.duplicateJob)
	mux.HandleFunc("GET /api/events", a.streamEvents)
	mux.HandleFunc("GET /api/system", a.system)
	mux.HandleFunc("GET /artifacts/{id}/{path...}", a.artifact)
	mux.Handle("/", static)
	return securityHeaders(requestLog(logger, mux)), nil
}

func (a *API) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) engines(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.registry.All())
}

func (a *API) listJobs(w http.ResponseWriter, r *http.Request) {
	items, err := a.repo.List(r.Context(), 100)
	if err != nil {
		a.internalError(w, "list jobs", err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (a *API) getJob(w http.ResponseWriter, r *http.Request) {
	job, err := a.repo.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, database.ErrNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Job not found")
		return
	}
	if err != nil {
		a.internalError(w, "get job", err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (a *API) createJob(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "CROSS_ORIGIN_REQUEST", "Cross-origin requests are not allowed")
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "INVALID_CONTENT_TYPE", "Content-Type must be application/json")
		return
	}
	var request jobs.CreateRequest
	if err := decodeJSON(w, r, &request); err != nil {
		return
	}
	job, err := a.newJob(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PARAMETERS", err.Error())
		return
	}
	if err := a.repo.Create(r.Context(), job); err != nil {
		a.internalError(w, "create job", err)
		return
	}
	if err := a.orchestrator.Enqueue(job.ID); err != nil {
		job.Status, job.ErrorCode, job.ErrorMessage = jobs.Failed, "QUEUE_FULL", err.Error()
		now := time.Now().UTC()
		job.CompletedAt = &now
		_ = a.repo.Save(r.Context(), job)
		writeError(w, http.StatusServiceUnavailable, "QUEUE_FULL", "The job queue is full")
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (a *API) duplicateJob(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "CROSS_ORIGIN_REQUEST", "Cross-origin requests are not allowed")
		return
	}
	original, err := a.repo.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, database.ErrNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Job not found")
		return
	}
	if err != nil {
		a.internalError(w, "duplicate job", err)
		return
	}
	seed := original.Seed
	job, err := a.newJob(jobs.CreateRequest{Engine: original.Engine, Parameters: original.Parameters, Seed: &seed})
	if err != nil {
		a.internalError(w, "duplicate job", err)
		return
	}
	if err := a.repo.Create(r.Context(), job); err != nil {
		a.internalError(w, "save duplicate", err)
		return
	}
	if err := a.orchestrator.Enqueue(job.ID); err != nil {
		job.Status, job.ErrorCode, job.ErrorMessage = jobs.Failed, "QUEUE_FULL", err.Error()
		now := time.Now().UTC()
		job.CompletedAt = &now
		_ = a.repo.Save(r.Context(), job)
		writeError(w, http.StatusServiceUnavailable, "QUEUE_FULL", "The job queue is full")
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (a *API) newJob(request jobs.CreateRequest) (jobs.Job, error) {
	if len(request.Parameters) == 0 {
		request.Parameters = json.RawMessage(`{}`)
	}
	if err := jobs.ValidateCreate(request); err != nil {
		return jobs.Job{}, err
	}
	manifest, ok := a.registry.Get(request.Engine)
	if !ok {
		return jobs.Job{}, errors.New("unsupported engine")
	}
	now := time.Now().UTC()
	id, err := jobs.NewID(now)
	if err != nil {
		return jobs.Job{}, err
	}
	seed := randomSeed()
	if request.Seed != nil {
		seed = *request.Seed
	}
	return jobs.Job{ID: id, Engine: request.Engine, Status: jobs.Queued, Parameters: request.Parameters, Seed: seed, CreatedAt: now, EngineVersion: manifest.Version, RuntimeDevice: a.cfg.Runtime.Device, RuntimePrecision: a.cfg.Runtime.DefaultPrecision}, nil
}

func (a *API) cancelJob(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "CROSS_ORIGIN_REQUEST", "Cross-origin requests are not allowed")
		return
	}
	err := a.orchestrator.Cancel(r.Context(), r.PathValue("id"))
	if errors.Is(err, database.ErrNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Job not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, "CANNOT_CANCEL", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "cancelling"})
}

func (a *API) streamEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "STREAM_UNAVAILABLE", "Streaming is unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, unsubscribe := a.broker.Subscribe()
	defer unsubscribe()
	io.WriteString(w, ": connected\n\n")
	flusher.Flush()
	keepAlive := time.NewTicker(20 * time.Second)
	defer keepAlive.Stop()
	filter := r.URL.Query().Get("job_id")
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepAlive.C:
			io.WriteString(w, ": keep-alive\n\n")
			flusher.Flush()
		case event := <-ch:
			if filter != "" && event.JobID != filter {
				continue
			}
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Event, data)
			flusher.Flush()
		}
	}
}

func (a *API) artifact(w http.ResponseWriter, r *http.Request) {
	id, relative := r.PathValue("id"), filepath.Clean(r.PathValue("path"))
	if !jobIDPattern.MatchString(id) || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		http.NotFound(w, r)
		return
	}
	jobsRoot, err := filepath.EvalSymlinks(a.cfg.JobRoot())
	if err != nil {
		http.NotFound(w, r)
		return
	}
	root := filepath.Join(jobsRoot, id)
	target := filepath.Join(root, relative)
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if resolvedRoot != jobsRoot && !strings.HasPrefix(resolvedRoot, jobsRoot+string(filepath.Separator)) {
		http.NotFound(w, r)
		return
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil || (resolved != resolvedRoot && !strings.HasPrefix(resolved, resolvedRoot+string(filepath.Separator))) {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	ext := strings.ToLower(filepath.Ext(resolved))
	if contentType := mime.TypeByExtension(ext); strings.HasPrefix(contentType, "image/") || strings.HasPrefix(contentType, "video/") {
		w.Header().Set("Content-Type", contentType)
	} else {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeFile(w, r, resolved)
}

func (a *API) system(w http.ResponseWriter, r *http.Request) {
	a.systemMu.Lock()
	defer a.systemMu.Unlock()
	if a.systemCache != nil && time.Since(a.systemCached) < 30*time.Second {
		writeJSON(w, http.StatusOK, a.systemCache)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, a.cfg.Runtime.PythonExecutable, "-m", "legacy_lab.runner", "--self-test", "--device", a.cfg.Runtime.Device)
	command.Env = append(os.Environ(), "PYTHONPATH="+a.cfg.Runtime.PythonPath)
	output, err := command.Output()
	probe := map[string]any{"available": false, "error": "Runtime probe failed"}
	if err == nil {
		if decodeErr := json.Unmarshal(output, &probe); decodeErr != nil {
			probe["error"] = "Runtime returned invalid data"
		}
	}
	response := map[string]any{"application_version": a.version, "go_version": runtime.Version(), "runtime": probe, "configured_device": a.cfg.Runtime.Device, "queue_length": a.orchestrator.QueueLength(), "models_directory": a.cfg.Paths.Models, "workspace_directory": a.cfg.Paths.Workspace}
	a.systemCache, a.systemCached = response, time.Now()
	writeJSON(w, http.StatusOK, response)
}

func (a *API) internalError(w http.ResponseWriter, operation string, err error) {
	a.logger.Error(operation, "error", err)
	writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "The request could not be completed")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "Request body must contain valid JSON")
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "Request body must contain one JSON object")
		return errors.New("trailing JSON data")
	}
	return nil
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && strings.EqualFold(parsed.Host, r.Host)
}

func randomSeed() int64 {
	var data [8]byte
	if _, err := rand.Read(data[:]); err != nil {
		return time.Now().UnixNano()
	}
	return int64(binary.LittleEndian.Uint64(data[:]) & (1<<63 - 1))
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; script-src 'self'; style-src 'self'; connect-src 'self'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func requestLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("HTTP request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(started).Milliseconds())
	})
}
