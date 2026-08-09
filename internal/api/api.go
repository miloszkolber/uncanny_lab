package api

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
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
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/miloszkolber/uncanny-lab/internal/config"
	"github.com/miloszkolber/uncanny-lab/internal/database"
	"github.com/miloszkolber/uncanny-lab/internal/engines"
	"github.com/miloszkolber/uncanny-lab/internal/events"
	"github.com/miloszkolber/uncanny-lab/internal/jobs"
	"github.com/miloszkolber/uncanny-lab/internal/modelinstall"
	"github.com/miloszkolber/uncanny-lab/internal/orchestrator"
	webassets "github.com/miloszkolber/uncanny-lab/web"
)

type API struct {
	repo         *database.Repository
	orchestrator *orchestrator.Orchestrator
	broker       *events.Broker
	cfg          config.Config
	version      string
	logger       *slog.Logger
	registry     *engines.Registry
	installer    *modelinstall.Manager
	verifySem    chan struct{}
	systemMu     sync.Mutex
	systemCache  map[string]any
	systemCached time.Time
}

var jobIDPattern = regexp.MustCompile(`^[0-9a-f]+-[0-9a-f]{12}$`)
var uploadIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var previewPathPattern = regexp.MustCompile(`^previews/[0-9]{6}\.png$`)

func New(repo *database.Repository, runner *orchestrator.Orchestrator, broker *events.Broker, cfg config.Config, version string, logger *slog.Logger, installer *modelinstall.Manager) (http.Handler, error) {
	static, err := webassets.Handler()
	if err != nil {
		return nil, err
	}
	registry, err := engines.Load(cfg.Paths.Manifests)
	if err != nil {
		return nil, err
	}
	a := &API{repo: repo, orchestrator: runner, broker: broker, cfg: cfg, version: version, logger: logger, registry: registry, verifySem: make(chan struct{}, 2), installer: installer}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /api/engines", a.engines)
	mux.HandleFunc("GET /api/models", a.models)
	mux.HandleFunc("GET /api/model-installer", a.modelInstaller)
	mux.HandleFunc("POST /api/model-installer/install", a.installModels)
	mux.HandleFunc("POST /api/model-installer/cancel", a.cancelInstall)
	mux.HandleFunc("POST /api/models/{id}/verify", a.verifyModel)
	mux.HandleFunc("POST /api/uploads", a.upload)
	mux.HandleFunc("GET /api/uploads/{token}", a.getUpload)
	mux.HandleFunc("GET /api/jobs", a.listJobs)
	mux.HandleFunc("POST /api/jobs", a.createJob)
	mux.HandleFunc("GET /api/jobs/{id}", a.getJob)
	mux.HandleFunc("POST /api/jobs/{id}/cancel", a.cancelJob)
	mux.HandleFunc("POST /api/jobs/{id}/duplicate", a.duplicateJob)
	mux.HandleFunc("POST /api/jobs/{id}/use-as-input", a.useAsInput)
	mux.HandleFunc("GET /api/jobs/{id}/export", a.exportJob)
	mux.HandleFunc("DELETE /api/jobs/{id}", a.deleteJob)
	mux.HandleFunc("GET /api/events", a.streamEvents)
	mux.HandleFunc("GET /api/system", a.system)
	mux.HandleFunc("GET /artifacts/{id}/{path...}", a.artifact)
	mux.Handle("/", static)
	return securityHeaders(requestLog(logger, hostGuard(cfg.Server.AllowedHosts, mux))), nil
}

func (a *API) modelInstaller(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.installer == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "capability": map[string]any{"available": false, "message": "Installer is unavailable"}})
		return
	}
	available := a.installer.Enabled()
	message := "Checkpoint downloads are disabled"
	if available {
		message = "Bundle B installer is available"
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": available, "capability": map[string]any{"available": available, "message": message}, "policy": map[string]string{"version": modelinstall.PolicyVersion, "text": modelinstall.PolicyText}, "catalog": map[string]any{"version": modelinstall.CatalogVersion, "files": modelinstall.Sources, "repositories": modelinstall.Repos, "outputs": modelinstall.Outputs, "estimated_disk_bytes": int64(6 << 30)}, "operation": a.installer.Latest(), "installed": a.installer.Installed()})
}

type installRequest struct {
	Accepted      bool   `json:"accepted"`
	PolicyVersion string `json:"policy_version"`
}
type cancelInstallRequest struct {
	OperationID string `json:"operation_id"`
}

func (a *API) installModels(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, 403, "CROSS_ORIGIN_REQUEST", "Cross-origin requests are not allowed")
		return
	}
	if !isJSONContentType(r) {
		writeError(w, 415, "INVALID_CONTENT_TYPE", "Content-Type must be application/json")
		return
	}
	if a.installer == nil || !a.installer.Enabled() {
		writeError(w, 403, "DOWNLOADS_DISABLED", "Checkpoint downloads are disabled")
		return
	}
	var q installRequest
	if err := decodeSmallJSON(w, r, &q); err != nil {
		return
	}
	if !q.Accepted {
		writeError(w, 400, "POLICY_NOT_ACCEPTED", "Policy acceptance is required")
		return
	}
	if q.PolicyVersion != modelinstall.PolicyVersion {
		writeError(w, 409, "STALE_POLICY", "Reload the policy before installing")
		return
	}
	op, err := a.installer.Start(time.Now().UTC())
	if err != nil {
		a.installError(w, err)
		return
	}
	writeJSON(w, 202, op)
}
func (a *API) cancelInstall(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, 403, "CROSS_ORIGIN_REQUEST", "Cross-origin requests are not allowed")
		return
	}
	if !isJSONContentType(r) {
		writeError(w, 415, "INVALID_CONTENT_TYPE", "Content-Type must be application/json")
		return
	}
	var q cancelInstallRequest
	if err := decodeSmallJSON(w, r, &q); err != nil {
		return
	}
	if a.installer == nil {
		writeError(w, 503, "INSTALLER_UNAVAILABLE", "Installer is unavailable")
		return
	}
	if err := a.installer.Cancel(q.OperationID); err != nil {
		writeError(w, 409, "CANNOT_CANCEL", "No matching active installation")
		return
	}
	writeJSON(w, 202, map[string]string{"status": "cancelling"})
}
func isJSONContentType(r *http.Request) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && media == "application/json"
}

func isMultipartContentType(r *http.Request) bool {
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && media == "multipart/form-data" && params["boundary"] != ""
}
func (a *API) installError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, modelinstall.ErrDisabled):
		writeError(w, 403, "DOWNLOADS_DISABLED", "Checkpoint downloads are disabled")
	case errors.Is(e, modelinstall.ErrStorage):
		writeError(w, 507, "INSUFFICIENT_STORAGE", "At least 6 GiB free space is required")
	case errors.Is(e, modelinstall.ErrAlreadyInstalled):
		writeError(w, 409, "ALREADY_INSTALLED", "Bundle B is already installed for this catalog")
	case errors.Is(e, modelinstall.ErrActive), errors.Is(e, modelinstall.ErrPhysicalBundle):
		writeError(w, 409, "INSTALLATION_CONFLICT", "An installation or non-atomic bundle already exists")
	default:
		a.logger.Error("start installer", "error", e)
		writeError(w, 503, "INSTALLER_UNAVAILABLE", "Installer is unavailable")
	}
}
func decodeSmallJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(target); e != nil {
		writeError(w, 400, "INVALID_JSON", "Request body must contain valid JSON")
		return e
	}
	if e := d.Decode(&struct{}{}); !errors.Is(e, io.EOF) {
		writeError(w, 400, "INVALID_JSON", "Request body must contain one JSON object")
		return errors.New("trailing data")
	}
	return nil
}

func (a *API) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) engines(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.registry.Enabled())
}

func (a *API) models(w http.ResponseWriter, _ *http.Request) {
	models, err := engines.LoadModels(a.cfg.Paths.Models, a.registry)
	if err != nil {
		a.internalError(w, "list models", err)
		return
	}
	writeJSON(w, http.StatusOK, models)
}

func (a *API) verifyModel(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "CROSS_ORIGIN_REQUEST", "Cross-origin requests are not allowed")
		return
	}
	select {
	case a.verifySem <- struct{}{}:
		defer func() { <-a.verifySem }()
	case <-r.Context().Done():
		return
	}
	model, err := engines.VerifyModel(a.cfg.Paths.Models, r.PathValue("id"), a.registry)
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Model not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_MODEL", "Model could not be verified")
		return
	}
	writeJSON(w, http.StatusOK, model)
}

const maxUploadBytes = 32 << 20
const maxImagePixels = 40_000_000
const maxImageDimension = 12_000

func (a *API) upload(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "CROSS_ORIGIN_REQUEST", "Cross-origin requests are not allowed")
		return
	}
	if !isMultipartContentType(r) {
		writeError(w, http.StatusUnsupportedMediaType, "INVALID_CONTENT_TYPE", "Content-Type must be multipart/form-data")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+1024)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "UPLOAD_TOO_LARGE", "Upload exceeds 32 MiB")
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, _, err := r.FormFile("image")
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_UPLOAD", "A form field named image is required")
		return
	}
	defer file.Close()
	decoded, format, err := image.DecodeConfig(io.LimitReader(file, maxUploadBytes))
	if err != nil || (format != "png" && format != "jpeg") {
		writeError(w, http.StatusUnsupportedMediaType, "INVALID_IMAGE", "Upload must be a PNG or JPEG image")
		return
	}
	if decoded.Width < 1 || decoded.Height < 1 || decoded.Width > maxImageDimension || decoded.Height > maxImageDimension || int64(decoded.Width)*int64(decoded.Height) > maxImagePixels {
		writeError(w, http.StatusBadRequest, "IMAGE_TOO_LARGE", "Image dimensions exceed limits")
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		a.internalError(w, "rewind upload", err)
		return
	}
	img, _, err := image.Decode(io.LimitReader(file, maxUploadBytes))
	if err != nil {
		writeError(w, http.StatusUnsupportedMediaType, "INVALID_IMAGE", "Upload must be a PNG or JPEG image")
		return
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		a.internalError(w, "generate upload ID", err)
		return
	}
	id := hex.EncodeToString(random[:])
	if err := os.MkdirAll(a.cfg.Paths.Inputs, 0o750); err != nil {
		a.internalError(w, "create inputs directory", err)
		return
	}
	target := filepath.Join(a.cfg.Paths.Inputs, id+".png")
	temporary := target + ".tmp"
	out, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err == nil {
		err = png.Encode(out, img)
		closeErr := out.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err != nil {
		_ = os.Remove(temporary)
		a.internalError(w, "save upload", err)
		return
	}
	if err := os.Rename(temporary, target); err != nil {
		_ = os.Remove(temporary)
		a.internalError(w, "publish upload", err)
		return
	}
	token := "inputs/" + id + ".png"
	writeJSON(w, http.StatusCreated, map[string]string{"token": token, "url": "/api/uploads/" + id})
}

func (a *API) getUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("token")
	if !uploadIDPattern.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	path, err := containedFile(a.cfg.Paths.Inputs, id+".png")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeFile(w, r, path)
}

func (a *API) listJobs(w http.ResponseWriter, r *http.Request) {
	items, err := a.repo.List(r.Context(), 100)
	if err != nil {
		a.internalError(w, "list jobs", err)
		return
	}
	for index := range items {
		a.attachFrames(&items[index])
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
	a.attachFrames(&job)
	writeJSON(w, http.StatusOK, job)
}

func (a *API) createJob(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "CROSS_ORIGIN_REQUEST", "Cross-origin requests are not allowed")
		return
	}
	if !isJSONContentType(r) {
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
		if persistErr := a.orchestrator.MarkFailed(&job, "QUEUE_FULL", err.Error()); persistErr != nil {
			if saveErr := a.repo.Save(r.Context(), job); saveErr != nil {
				a.logger.Error("save queue failure after metadata error", "job_id", job.ID, "error", saveErr)
			}
			a.internalError(w, "persist queue failure", persistErr)
			return
		}
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
	parameters, err := a.duplicateParameters(original)
	if err != nil {
		a.internalError(w, "preserve duplicate inputs", err)
		return
	}
	seed := original.Seed
	job, err := a.newJob(jobs.CreateRequest{Engine: original.Engine, Parameters: parameters, Seed: &seed})
	if err != nil {
		a.internalError(w, "duplicate job", err)
		return
	}
	if err := a.repo.Create(r.Context(), job); err != nil {
		a.internalError(w, "save duplicate", err)
		return
	}
	if err := a.orchestrator.Enqueue(job.ID); err != nil {
		if persistErr := a.orchestrator.MarkFailed(&job, "QUEUE_FULL", err.Error()); persistErr != nil {
			if saveErr := a.repo.Save(r.Context(), job); saveErr != nil {
				a.logger.Error("save duplicate queue failure after metadata error", "job_id", job.ID, "error", saveErr)
			}
			a.internalError(w, "persist duplicate queue failure", persistErr)
			return
		}
		writeError(w, http.StatusServiceUnavailable, "QUEUE_FULL", "The job queue is full")
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (a *API) duplicateParameters(original jobs.Job) (json.RawMessage, error) {
	var parameters map[string]any
	if err := json.Unmarshal(original.Parameters, &parameters); err != nil {
		return nil, err
	}
	jobDir, err := jobDirectory(a.cfg, original.ID)
	if err != nil {
		return original.Parameters, nil
	}
	for _, key := range []string{"source_image", "style_image", "init_image"} {
		if _, exists := parameters[key]; !exists {
			continue
		}
		source, err := containedFile(jobDir, filepath.Join("inputs", key+".png"))
		if err != nil {
			continue
		}
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, err
		}
		id := hex.EncodeToString(random[:])
		target := filepath.Join(a.cfg.Paths.Inputs, id+".png")
		if err := copyFile(source, target); err != nil {
			return nil, err
		}
		parameters[key] = "inputs/" + id + ".png"
	}
	return json.Marshal(parameters)
}

func copyFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	temporary := target + ".tmp"
	out, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(temporary)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(temporary)
		return closeErr
	}
	return os.Rename(temporary, target)
}

func (a *API) useAsInput(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "CROSS_ORIGIN_REQUEST", "Cross-origin requests are not allowed")
		return
	}
	job, err := a.repo.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, database.ErrNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Job not found")
		return
	}
	if err != nil {
		a.internalError(w, "get job input", err)
		return
	}
	artifactPath := r.URL.Query().Get("path")
	if artifactPath == "" {
		artifactPath = "final.png"
	}
	if !validInputArtifact(artifactPath) {
		writeError(w, http.StatusBadRequest, "INVALID_ARTIFACT", "Selected image is not a job artifact")
		return
	}
	if artifactPath == "final.png" && (job.Status != jobs.Completed || job.FinalPath != "final.png") {
		writeError(w, http.StatusConflict, "NO_FINAL_IMAGE", "Job has no final image")
		return
	}
	directory, err := jobDirectory(a.cfg, job.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Job image not found")
		return
	}
	source, err := containedFile(directory, artifactPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Job image not found")
		return
	}
	if err := os.MkdirAll(a.cfg.Paths.Inputs, 0o750); err != nil {
		a.internalError(w, "create inputs directory", err)
		return
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		a.internalError(w, "create input token", err)
		return
	}
	id := hex.EncodeToString(random[:])
	if err := copyFile(source, filepath.Join(a.cfg.Paths.Inputs, id+".png")); err != nil {
		a.internalError(w, "preserve job input", err)
		return
	}
	token := "inputs/" + id + ".png"
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "template": map[string]any{"parameters": map[string]string{"source_image": token}}})
}

func validInputArtifact(path string) bool {
	return path == "final.png" || previewPathPattern.MatchString(path)
}

func (a *API) attachFrames(job *jobs.Job) {
	entries, err := os.ReadDir(filepath.Join(a.cfg.JobRoot(), job.ID, "previews"))
	if err != nil {
		return
	}
	frames := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".png") {
			frames = append(frames, "previews/"+entry.Name())
		}
	}
	sort.Strings(frames)
	job.PreviewFrames = frames
}

func (a *API) exportJob(w http.ResponseWriter, r *http.Request) {
	job, err := a.repo.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, database.ErrNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Job not found")
		return
	}
	if err != nil {
		a.internalError(w, "export job", err)
		return
	}
	if job.Status != jobs.Completed && job.Status != jobs.Failed && job.Status != jobs.Cancelled {
		writeError(w, http.StatusConflict, "JOB_NOT_TERMINAL", "Only terminal jobs can be exported")
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Minute))
	directory, err := jobDirectory(a.cfg, job.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Job directory not found")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "uncanny-lab-"+job.ID+".zip"))
	zipWriter := zip.NewWriter(w)
	err = filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return err
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		file, err := zipWriter.Create(filepath.ToSlash(relative))
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(file, in)
		closeErr := in.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if closeErr := zipWriter.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		a.logger.Error("export job", "job_id", job.ID, "error", err)
	}
}

func (a *API) deleteJob(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "CROSS_ORIGIN_REQUEST", "Cross-origin requests are not allowed")
		return
	}
	job, err := a.repo.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, database.ErrNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Job not found")
		return
	}
	if err != nil {
		a.internalError(w, "get job for deletion", err)
		return
	}
	if job.Status != jobs.Completed && job.Status != jobs.Failed && job.Status != jobs.Cancelled {
		writeError(w, http.StatusConflict, "JOB_NOT_TERMINAL", "Only terminal jobs can be deleted")
		return
	}
	directory, err := jobDirectory(a.cfg, job.ID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusBadRequest, "INVALID_JOB_DIRECTORY", "Job directory is unsafe")
		return
	}
	quarantine := ""
	if err == nil {
		var suffix [8]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			a.internalError(w, "prepare job deletion", err)
			return
		}
		quarantine = directory + ".deleting-" + hex.EncodeToString(suffix[:])
		if err := os.Rename(directory, quarantine); err != nil {
			a.internalError(w, "quarantine job directory", err)
			return
		}
	}
	if err := a.repo.Delete(r.Context(), job.ID); err != nil {
		if quarantine != "" {
			if restoreErr := os.Rename(quarantine, directory); restoreErr != nil {
				a.logger.Error("restore job directory after failed deletion", "job_id", job.ID, "error", restoreErr)
			}
		}
		a.internalError(w, "delete job", err)
		return
	}
	if quarantine != "" {
		if err := os.RemoveAll(quarantine); err != nil {
			if createErr := a.repo.Create(r.Context(), job); createErr == nil {
				_ = os.Rename(quarantine, directory)
			}
			a.internalError(w, "remove job directory", err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) newJob(request jobs.CreateRequest) (jobs.Job, error) {
	if len(request.Parameters) == 0 {
		request.Parameters = json.RawMessage(`{}`)
	}
	if err := jobs.ValidateCreate(request); err != nil {
		return jobs.Job{}, err
	}
	manifest, ok := a.registry.Get(request.Engine)
	if !ok || !manifest.IsEnabled() {
		return jobs.Job{}, errors.New("unsupported engine")
	}
	models, err := engines.LoadModels(a.cfg.Paths.Models, a.registry)
	if err != nil {
		return jobs.Job{}, fmt.Errorf("inspect required models: %w", err)
	}
	statuses := make(map[string]string, len(models))
	for _, model := range models {
		statuses[model.ID] = model.Status
	}
	var missing []string
	for _, id := range manifest.Models {
		if statuses[id] != "available" {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return jobs.Job{}, fmt.Errorf("required models are unavailable: %s", strings.Join(missing, ", "))
	}
	parameters, err := manifest.ApplyDefaults(request.Parameters)
	if err != nil {
		return jobs.Job{}, err
	}
	if err := a.validateRequiredInputs(manifest, parameters); err != nil {
		return jobs.Job{}, err
	}
	request.Parameters = parameters
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

func (a *API) validateRequiredInputs(manifest engines.Manifest, parameters json.RawMessage) error {
	if len(manifest.RequiredInputs) == 0 {
		return nil
	}
	values := make(map[string]any)
	if err := json.Unmarshal(parameters, &values); err != nil {
		return fmt.Errorf("decode parameters: %w", err)
	}
	for _, required := range manifest.RequiredInputs {
		value, ok := values[required].(string)
		if !ok || strings.TrimSpace(value) == "" || !a.validRequiredImageToken(value) {
			return fmt.Errorf("required image input %q is missing", required)
		}
	}
	return nil
}

func (a *API) validRequiredImageToken(token string) bool {
	if strings.HasPrefix(token, "inputs/") {
		relative := strings.TrimPrefix(token, "inputs/")
		if filepath.Ext(relative) != ".png" || !uploadIDPattern.MatchString(strings.TrimSuffix(relative, ".png")) {
			return false
		}
		_, err := containedFile(a.cfg.Paths.Inputs, relative)
		return err == nil
	}
	if !strings.HasPrefix(token, "workspace/jobs/") {
		return false
	}
	relative := strings.TrimPrefix(token, "workspace/jobs/")
	parts := strings.Split(filepath.ToSlash(relative), "/")
	validArtifact := len(parts) == 2 && parts[1] == "final.png" || len(parts) == 3 && parts[1] == "previews" && previewPathPattern.MatchString(parts[1]+"/"+parts[2])
	if len(parts) < 2 || !jobIDPattern.MatchString(parts[0]) || !validArtifact {
		return false
	}
	_, err := containedFile(a.cfg.JobRoot(), filepath.FromSlash(relative))
	return err == nil
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
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
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
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(relative)))
	}
	http.ServeFile(w, r, resolved)
}

func (a *API) system(w http.ResponseWriter, r *http.Request) {
	a.systemMu.Lock()
	var probe map[string]any
	if a.systemCache != nil && time.Since(a.systemCached) < 30*time.Second {
		probe, _ = a.systemCache["runtime"].(map[string]any)
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		command := exec.CommandContext(ctx, a.cfg.Runtime.PythonExecutable, "-m", "uncanny_lab.runner", "--self-test", "--device", a.cfg.Runtime.Device)
		command.Env = append(os.Environ(), "PYTHONPATH="+a.cfg.Runtime.PythonPath, "UNCANNY_DATA_ROOT="+a.cfg.Paths.Data, "UNCANNY_MODELS_ROOT="+a.cfg.Paths.Models)
		output, err := command.Output()
		cancel()
		probe = map[string]any{}
		if err == nil {
			if decodeErr := json.Unmarshal(output, &probe); decodeErr != nil {
				probe["error"] = "Runtime returned invalid data"
			}
		} else {
			probe["available"] = false
			probe["error"] = "Runtime probe failed"
		}
		a.systemCache, a.systemCached = map[string]any{"runtime": probe}, time.Now()
	}
	a.systemMu.Unlock()
	queue, _ := a.repo.List(r.Context(), 200)
	states := map[string]int{}
	for _, job := range queue {
		states[string(job.Status)]++
	}
	response := map[string]any{"application_name": "Uncanny Lab", "application_version": a.version, "go_version": runtime.Version(), "runtime": probe, "configured_device": a.cfg.Runtime.Device, "queue_length": a.orchestrator.QueueLength(), "job_states": states, "models_directory": a.cfg.Paths.Models, "workspace_directory": a.cfg.Paths.Workspace, "data_free_bytes": freeSpace(a.cfg.Paths.Data)}
	writeJSON(w, http.StatusOK, response)
}

func (a *API) internalError(w http.ResponseWriter, operation string, err error) {
	a.logger.Error(operation, "error", err)
	writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "The request could not be completed")
}

func containedFile(root, relative string) (string, error) {
	if filepath.IsAbs(relative) || filepath.Clean(relative) != relative {
		return "", errors.New("invalid path")
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	path, err := filepath.EvalSymlinks(filepath.Join(resolvedRoot, relative))
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(path, resolvedRoot+string(filepath.Separator)) {
		return "", errors.New("path escapes root")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("not regular")
	}
	return path, nil
}

func jobDirectory(cfg config.Config, id string) (string, error) {
	if !jobIDPattern.MatchString(id) {
		return "", errors.New("invalid job ID")
	}
	root, err := filepath.EvalSymlinks(cfg.JobRoot())
	if err != nil {
		return "", err
	}
	directory := filepath.Join(root, id)
	info, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("job directory is unsafe")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return "", errors.New("job directory escapes root")
	}
	return resolved, nil
}

func freeSpace(path string) int64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0
	}
	return int64(stat.Bavail) * int64(stat.Bsize)
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

func hostGuard(hosts []string, next http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		allowed[strings.ToLower(host)] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := allowed[strings.ToLower(r.Host)]; !ok {
			writeError(w, http.StatusMisdirectedRequest, "UNTRUSTED_HOST", "Request host is not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
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
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; script-src 'self'; style-src 'self'; connect-src 'self'")
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
