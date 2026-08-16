package modelinstall

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const minimumFree = int64(6 << 30)

type Phase string

const (
	Idle        Phase = "idle"
	Downloading Phase = "downloading"
	Converting  Phase = "converting"
	Verifying   Phase = "verifying"
	Succeeded   Phase = "succeeded"
	Failed      Phase = "failed"
	Cancelled   Phase = "cancelled"
)

type Operation struct {
	ID               string    `json:"id"`
	Status           string    `json:"status"`
	Phase            Phase     `json:"phase"`
	CurrentSource    string    `json:"current_source,omitempty"`
	CompletedBytes   int64     `json:"completed_bytes"`
	TotalBytes       int64     `json:"total_bytes"`
	CompletedSources int       `json:"completed_sources"`
	TotalSources     int       `json:"total_sources"`
	StartedAt        time.Time `json:"started_at,omitempty"`
	FinishedAt       time.Time `json:"finished_at,omitempty"`
	ErrorCode        string    `json:"error_code,omitempty"`
	ErrorMessage     string    `json:"error_message,omitempty"`
	ProvenancePath   string    `json:"provenance_path,omitempty"`
	PolicyAcceptedAt time.Time `json:"policy_accepted_at,omitempty"`
	CandidateTarget  string    `json:"candidate_target,omitempty"`
	PriorTarget      string    `json:"prior_target,omitempty"`
}
type Manager struct {
	enabled                     bool
	workspace, models, revision string
	logger                      *slog.Logger
	mu                          sync.Mutex
	op                          Operation
	cancel                      context.CancelFunc
	done                        chan struct{}
	lock                        *os.File
	client                      *http.Client
	unavailable                 bool
	publicationCommitted        bool
}

type installError struct {
	code, message string
	err           error
}

func (e *installError) Error() string               { return e.err.Error() }
func (e *installError) Unwrap() error               { return e.err }
func failure(code, message string, err error) error { return &installError{code, message, err} }

func New(enabled bool, workspace, models, revision string, logger *slog.Logger) (*Manager, error) {
	m := &Manager{enabled: enabled, workspace: workspace, models: models, revision: revision, logger: logger, client: secureClient(), op: Operation{Phase: Idle, Status: "idle"}}
	if err := os.MkdirAll(m.root(), 0750); err != nil {
		return nil, err
	}
	if err := m.recover(); err != nil {
		return nil, err
	}
	return m, nil
}
func (m *Manager) root() string      { return filepath.Join(m.workspace, "model-installer") }
func (m *Manager) statePath() string { return filepath.Join(m.root(), "latest-operation.json") }
func (m *Manager) recover() error {
	lock, err := os.OpenFile(filepath.Join(m.root(), "operation.lock"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		m.unavailable = true
		return nil
	}
	defer func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
	}()
	b, e := os.ReadFile(m.statePath())
	if e != nil {
		if errors.Is(e, os.ErrNotExist) {
			return nil
		}
		return e
	}
	var op Operation
	if json.Unmarshal(b, &op) != nil || !validPersistedOperation(op) {
		m.op = Operation{Phase: Idle, Status: "idle"}
		return m.persist()
	}
	m.op = op
	if op.Status != "running" {
		return nil
	}
	stable := filepath.Join(m.models, "bundle-b")
	if op.CandidateTarget != "" {
		if target, err := os.Readlink(stable); err == nil && target == op.CandidateTarget && verifyBundleWithRevision(stable, &op, m.revision, true, true) == nil {
			m.op.Phase, m.op.Status = Succeeded, "succeeded"
			m.op.FinishedAt = time.Now().UTC()
			return m.persist()
		}
		if target, err := os.Readlink(stable); err == nil && target == op.CandidateTarget {
			if validBundleTarget(m.models, op.PriorTarget) {
				if err := restoreBundle(m.models, op.PriorTarget); err != nil {
					return err
				}
			} else if err := os.Remove(stable); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	m.op.Phase, m.op.Status = Failed, "failed"
	m.op.ErrorCode = "INTERRUPTED"
	m.op.ErrorMessage = "The previous installation was interrupted"
	m.op.FinishedAt = time.Now().UTC()
	m.removeOperationArtifacts(op)
	return m.persist()
}
func validPersistedOperation(op Operation) bool {
	if op.Status != "running" && op.Status != "idle" && op.Status != "succeeded" && op.Status != "failed" && op.Status != "cancelled" {
		return false
	}
	if op.Status == "running" && !validOperationID(op.ID) {
		return false
	}
	return op.Phase == Idle || op.Phase == Downloading || op.Phase == Converting || op.Phase == Verifying || op.Phase == Succeeded || op.Phase == Failed || op.Phase == Cancelled
}
func (m *Manager) Enabled() bool     { return m.enabled && !m.unavailable }
func (m *Manager) Latest() Operation { m.mu.Lock(); defer m.mu.Unlock(); return m.op }
func (m *Manager) Installed() bool {
	return verifyBundleWithRevision(filepath.Join(m.models, "bundle-b"), nil, "", false, false) == nil
}
func (m *Manager) Start(acceptedAt time.Time) (Operation, error) {
	if m.unavailable {
		return Operation{}, ErrUnavailable
	}
	if !m.enabled {
		return Operation{}, ErrDisabled
	}
	if m.Installed() {
		return Operation{}, ErrAlreadyInstalled
	}
	if free(m.workspace) < minimumFree || free(m.models) < minimumFree {
		return Operation{}, ErrStorage
	}
	if i, e := os.Lstat(filepath.Join(m.models, "bundle-b")); e == nil && i.IsDir() && i.Mode()&os.ModeSymlink == 0 {
		return Operation{}, ErrPhysicalBundle
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		return Operation{}, ErrActive
	}
	f, e := os.OpenFile(filepath.Join(m.root(), "operation.lock"), os.O_CREATE|os.O_WRONLY, 0600)
	if e != nil {
		return Operation{}, ErrActive
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return Operation{}, ErrActive
	}
	m.lock = f
	var raw [16]byte
	if _, e = rand.Read(raw[:]); e != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return Operation{}, fmt.Errorf("generate operation ID: %w", e)
	}
	ctx, c := context.WithCancel(context.Background())
	m.cancel = c
	m.done = make(chan struct{})
	m.op = Operation{ID: hex.EncodeToString(raw[:]), Status: "running", Phase: Downloading, TotalSources: len(Sources), TotalBytes: totalBytes(), StartedAt: time.Now().UTC(), PolicyAcceptedAt: acceptedAt}
	if e = m.persist(); e != nil {
		m.cancel = nil
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		m.lock = nil
		return Operation{}, fmt.Errorf("persist operation: %w", e)
	}
	go m.run(ctx)
	return m.op, nil
}
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var (
	ErrDisabled         = errors.New("disabled")
	ErrActive           = errors.New("active")
	ErrStorage          = errors.New("storage")
	ErrPhysicalBundle   = errors.New("physical bundle")
	ErrAlreadyInstalled = errors.New("already installed")
	ErrUnavailable      = errors.New("unavailable")
)

func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel == nil {
		return errors.New("no active operation")
	}
	if m.publicationCommitted {
		return errors.New("installation publication is already committed")
	}
	if id != m.op.ID {
		return errors.New("operation does not match")
	}
	m.cancel()
	return nil
}
func (m *Manager) run(ctx context.Context) {
	err := m.install(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if errors.Is(err, context.Canceled) {
		m.op.Phase = Cancelled
		m.op.Status = "cancelled"
		m.op.ErrorCode = "CANCELLED"
		m.op.ErrorMessage = "Installation cancelled"
	} else if err != nil {
		m.logger.Error("model installation failed", "error", err)
		m.op.Phase = Failed
		m.op.Status = "failed"
		var classified *installError
		if errors.As(err, &classified) {
			m.op.ErrorCode, m.op.ErrorMessage = classified.code, classified.message
		} else {
			m.op.ErrorCode, m.op.ErrorMessage = "CONVERSION", "Conversion failed. See server logs."
		}
	} else {
		m.op.Phase = Succeeded
		m.op.Status = "succeeded"
	}
	m.op.FinishedAt = time.Now().UTC()
	if m.op.Status == "failed" || m.op.Status == "cancelled" {
		m.removeOperationArtifacts(m.op)
	}
	if persistErr := m.persist(); persistErr != nil {
		m.logger.Error("persist installer result", "error", persistErr)
	}
	m.cancel = nil
	if m.lock != nil {
		syscall.Flock(int(m.lock.Fd()), syscall.LOCK_UN)
		m.lock.Close()
		m.lock = nil
	}
	if m.done != nil {
		close(m.done)
		m.done = nil
	}
	m.publicationCommitted = false
}
func (m *Manager) install(ctx context.Context) error {
	cache := filepath.Join(m.root(), "sources")
	for index, s := range Sources {
		m.progress(Downloading, s.Name)
		if err := m.download(ctx, s, filepath.Join(cache, s.Name)); err != nil {
			return err
		}
		m.mu.Lock()
		m.op.CompletedSources++
		m.op.CompletedBytes = sourceBytes(index + 1)
		if err := m.persist(); err != nil {
			m.logger.Error("persist installer progress", "error", err)
		}
		m.mu.Unlock()
	}
	m.progress(Converting, "Bundle B")
	op := m.Latest()
	args := []string{"/app/tools/convert_bundle_b.py", "--defer-publication", "--sources", cache, "--models", m.models, "--vgg-source", filepath.Join(cache, "vgg19.pt"), "--taming-source", "/app/conversion-sources/taming-transformers", "--biggan-source", "/app/conversion-sources/pytorch-pretrained-BigGAN", "--installer-catalog-version", CatalogVersion, "--policy-version", PolicyVersion, "--operation-id", op.ID, "--policy-accepted-at", op.PolicyAcceptedAt.Format(time.RFC3339), "--app-revision", m.revision}
	if err := os.MkdirAll(filepath.Join(m.root(), "tmp"), 0o750); err != nil {
		return failure("STORAGE", "Temporary conversion storage could not be prepared", err)
	}
	work, err := os.MkdirTemp(filepath.Join(m.root(), "tmp"), "conversion-")
	if err != nil {
		return failure("STORAGE", "Temporary conversion storage could not be prepared", err)
	}
	defer os.RemoveAll(work)
	env := []string{"PATH=/opt/venv/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/tmp", "XDG_CACHE_HOME=" + filepath.Join(work, "cache"), "PYTHONPATH=/app/tools", "PYTHONUNBUFFERED=1", "PYTHONDONTWRITEBYTECODE=1", "HF_HUB_OFFLINE=1", "HF_HUB_DISABLE_TELEMETRY=1", "HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9", "http_proxy=http://127.0.0.1:9", "https_proxy=http://127.0.0.1:9"}
	if err := runWithEnvDir(ctx, work, "python3", env, args...); err != nil {
		return failure("CONVERSION", "Conversion failed. See server logs.", err)
	}
	m.progress(Verifying, "Bundle B")
	candidate, err := m.candidate(op.ID)
	if err != nil {
		return failure("VERIFICATION", "Converted bundle candidate was not found", err)
	}
	check := filepath.Join(m.models, ".bundle-b-candidate-"+op.ID)
	if err := os.Symlink(filepath.Join("bundles", filepath.Base(candidate)), check); err != nil {
		return failure("STORAGE", "Candidate could not be checked", err)
	}
	defer os.Remove(check)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := verifyBundleWithRevisionContext(ctx, check, &op, m.revision, true, true); err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		return failure("VERIFICATION", "Installed bundle verification failed", err)
	}
	m.mu.Lock()
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return err
	}
	m.op.CandidateTarget = filepath.Join("bundles", filepath.Base(candidate))
	if old, err := os.Readlink(filepath.Join(m.models, "bundle-b")); err == nil {
		m.op.PriorTarget = old
	}
	if err := m.persist(); err != nil {
		m.mu.Unlock()
		return failure("STORAGE", "Installer operation state could not be persisted", err)
	}
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return err
	}
	m.publicationCommitted = true
	if err := swapBundleWithPrior(m.models, m.op.CandidateTarget, m.op.PriorTarget); err != nil {
		m.publicationCommitted = false
		m.mu.Unlock()
		return failure("STORAGE", "Bundle publication failed", err)
	}
	m.mu.Unlock()
	m.mu.Lock()
	m.op.ProvenancePath = filepath.Join(m.models, "bundle-b", "provenance/bundle-b-conversion-report.json")
	if err := m.persist(); err != nil {
		m.logger.Error("persist installer provenance", "error", err)
	}
	m.mu.Unlock()
	return nil
}
func (m *Manager) candidate(id string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(m.models, "bundles"))
	if err != nil {
		return "", err
	}
	const prefix = "bundle-b-"
	if len(id) < 12 {
		return "", os.ErrNotExist
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) && strings.HasSuffix(entry.Name(), "-"+id[:12]) {
			return filepath.Join(m.models, "bundles", entry.Name()), nil
		}
	}
	return "", os.ErrNotExist
}
func (m *Manager) removeCandidate(id string) {
	if len(id) < 12 {
		return
	}
	entries, err := os.ReadDir(filepath.Join(m.models, "bundles"))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "bundle-b-") && strings.HasSuffix(entry.Name(), "-"+id[:12]) {
			_ = os.RemoveAll(filepath.Join(m.models, "bundles", entry.Name()))
		}
	}
}
func (m *Manager) removeOperationArtifacts(op Operation) {
	if !validOperationID(op.ID) {
		return
	}
	_ = os.RemoveAll(filepath.Join(m.models, "bundles", ".bundle-b-"+op.ID+".staging"))
	// The converter publishes the version directory before this process can
	// persist CandidateTarget. Remove the operation-scoped version by ID as
	// well, so cancellation or a crash in that window cannot retain it.
	m.removeCandidate(op.ID)
	if op.CandidateTarget == "" || filepath.IsAbs(op.CandidateTarget) || filepath.Clean(op.CandidateTarget) != op.CandidateTarget || !strings.HasPrefix(op.CandidateTarget, "bundles/") {
		return
	}
	target := filepath.Join(m.models, op.CandidateTarget)
	if filepath.Dir(target) == filepath.Join(m.models, "bundles") && filepath.Base(target) != "bundle-b" {
		_ = os.RemoveAll(target)
	}
}
func swapBundleWithPrior(models, target, prior string) error {
	tmp := filepath.Join(models, ".bundle-b-publish")
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(models, "bundle-b")); err != nil {
		return err
	}
	d, err := os.Open(models)
	if err != nil {
		_ = restoreBundle(models, prior)
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		_ = restoreBundle(models, prior)
		return err
	}
	return nil
}
func restoreBundle(models, prior string) error {
	path := filepath.Join(models, "bundle-b")
	if prior == "" {
		return os.Remove(path)
	}
	tmp := filepath.Join(models, ".bundle-b-restore")
	_ = os.Remove(tmp)
	if err := os.Symlink(prior, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func validBundleTarget(models, target string) bool {
	if target == "" || filepath.IsAbs(target) || filepath.Clean(target) != target || !strings.HasPrefix(target, "bundles/") {
		return false
	}
	p := filepath.Join(models, target)
	i, err := os.Stat(p)
	return err == nil && i.IsDir() && filepath.Dir(p) == filepath.Join(models, "bundles") && filepath.Base(p) != "bundle-b"
}
func (m *Manager) progress(p Phase, s string) {
	m.mu.Lock()
	m.op.Phase = p
	m.op.CurrentSource = s
	if err := m.persist(); err != nil {
		m.logger.Error("persist installer phase", "error", err)
	}
	m.mu.Unlock()
}
func (m *Manager) persist() error {
	b, err := json.Marshal(m.op)
	if err != nil {
		return err
	}
	t := m.statePath() + ".part"
	f, err := os.OpenFile(t, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(t, m.statePath()); err != nil {
		return err
	}
	d, err := os.Open(m.root())
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func totalBytes() (n int64) {
	for _, s := range Sources {
		n += s.Bytes
	}
	return
}
func sourceBytes(count int) (n int64) {
	for i := 0; i < count && i < len(Sources); i++ {
		n += Sources[i].Bytes
	}
	return
}
func runWithEnvDir(ctx context.Context, dir, name string, env []string, args ...string) error {
	c := exec.CommandContext(ctx, name, args...)
	c.Env = env
	c.Dir = dir
	var output limitedBuffer
	c.Stdout, c.Stderr = &output, &output
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	select {
	case e := <-done:
		if e != nil && output.String() != "" {
			return fmt.Errorf("%w: %s", e, output.String())
		}
		return e
	case <-ctx.Done():
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		<-done
		return ctx.Err()
	}
}

type limitedBuffer struct{ b []byte }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	const max = 64 << 10
	if len(b.b) < max {
		b.b = append(b.b, p[:min(len(p), max-len(b.b))]...)
	}
	return len(p), nil
}
func (b *limitedBuffer) String() string { return string(b.b) }

func validOperationID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func verifyBundleWithRevision(p string, expected *Operation, revision string, strict, deep bool) error {
	return verifyBundleWithRevisionContext(context.Background(), p, expected, revision, strict, deep)
}
func verifyBundleWithRevisionContext(ctx context.Context, p string, expected *Operation, revision string, strict, deep bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	i, e := os.Lstat(p)
	if e != nil || i.Mode()&os.ModeSymlink == 0 {
		return errors.New("bundle was not atomically published")
	}
	root, e := filepath.EvalSymlinks(p)
	if e != nil || filepath.Dir(root) != filepath.Join(filepath.Dir(p), "bundles") {
		return errors.New("bundle target is unsafe")
	}
	for _, n := range []string{"classifiers/vgg19.pt", "clip/vit-b-32.pt", "vqgan/decoder.pt", "vqgan/codebook.pt", "biggan/generator.pt", "provenance/bundle-b-conversion-report.json"} {
		i, e = os.Stat(filepath.Join(p, n))
		if e != nil || !i.Mode().IsRegular() {
			return fmt.Errorf("missing artifact %s", n)
		}
	}
	var x provenance
	b, e := os.ReadFile(filepath.Join(p, "provenance/bundle-b-conversion-report.json"))
	if e != nil || json.Unmarshal(b, &x) != nil || x.Format != "uncanny-lab-bundle-b-conversion-v2" {
		return errors.New("invalid provenance")
	}
	if strict && (expected == nil || x.Installer.CatalogVersion != CatalogVersion || x.Installer.PolicyVersion != PolicyVersion || x.Installer.OperationID != expected.ID || x.Installer.AppRevision != revision || x.Installer.PolicyAcceptedAt != expected.PolicyAcceptedAt.UTC().Format(time.RFC3339)) {
		return errors.New("installer provenance mismatch")
	}
	if err := x.validateInputs(); err != nil {
		return err
	}
	for _, o := range Outputs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := verifyReportedArtifactContext(ctx, p, o.Path, x, deep); err != nil {
			return err
		}
	}
	return nil
}

type provenance struct {
	Format  string `json:"format"`
	Sources map[string]struct {
		URL string `json:"url"`
	} `json:"sources"`
	Installer struct {
		CatalogVersion   string `json:"catalog_version"`
		PolicyVersion    string `json:"policy_version"`
		OperationID      string `json:"operation_id"`
		PolicyAcceptedAt string `json:"policy_accepted_at"`
		AppRevision      string `json:"app_revision"`
	} `json:"installer"`
	VGG19 struct {
		Source   sourceRecord
		Artifact artifact
	} `json:"vgg19"`
	Clip struct {
		Source   sourceRecord
		Artifact artifact
	} `json:"clip"`
	VQGAN struct {
		Source     sourceRecord
		Config     sourceRecord
		SourceTree repoRecord `json:"source_tree"`
		Codebook   artifact
		Decoder    artifact
	} `json:"vqgan"`
	BigGAN struct {
		Weights    sourceRecord
		Config     sourceRecord
		SourceTree repoRecord `json:"source_tree"`
		Artifact   artifact
	} `json:"biggan"`
}
type artifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type sourceRecord struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type repoRecord struct {
	Commit string `json:"commit"`
	Tree   string `json:"tree"`
}

func (x provenance) validateInputs() error {
	records := []sourceRecord{x.VGG19.Source, x.Clip.Source, x.VQGAN.Source, x.VQGAN.Config, x.BigGAN.Weights, x.BigGAN.Config}
	for i, source := range Sources {
		if records[i].SHA256 != source.SHA256 || records[i].Bytes != source.Bytes {
			return fmt.Errorf("source provenance mismatch for %s", source.ID)
		}
	}
	if x.VQGAN.SourceTree.Commit != Repos[0].Commit || x.VQGAN.SourceTree.Tree != Repos[0].Tree || x.BigGAN.SourceTree.Commit != Repos[1].Commit || x.BigGAN.SourceTree.Tree != Repos[1].Tree {
		return errors.New("repository provenance mismatch")
	}
	for key, url := range map[string]string{"vgg19": Sources[0].URL, "clip": Sources[1].URL, "vqgan": Sources[2].URL, "biggan": Sources[4].URL} {
		if x.Sources[key].URL != url {
			return fmt.Errorf("source URL provenance mismatch for %s", key)
		}
	}
	return nil
}

func verifyReportedArtifact(root, path string, x provenance, deep bool) error {
	return verifyReportedArtifactContext(context.Background(), root, path, x, deep)
}
func verifyReportedArtifactContext(ctx context.Context, root, path string, x provenance, deep bool) error {
	var a artifact
	switch path {
	case "bundle-b/classifiers/vgg19.pt":
		a = x.VGG19.Artifact
	case "bundle-b/clip/vit-b-32.pt":
		a = x.Clip.Artifact
	case "bundle-b/vqgan/decoder.pt":
		a = x.VQGAN.Decoder
	case "bundle-b/vqgan/codebook.pt":
		a = x.VQGAN.Codebook
	case "bundle-b/biggan/generator.pt":
		a = x.BigGAN.Artifact
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		resolved = root
	}
	if a.Path != filepath.Join(filepath.Dir(filepath.Dir(resolved)), "bundle-b", strings.TrimPrefix(path, "bundle-b/")) {
		return fmt.Errorf("artifact path mismatch %s", path)
	}
	f := filepath.Join(root, strings.TrimPrefix(path, "bundle-b/"))
	i, e := os.Stat(f)
	if e != nil || !i.Mode().IsRegular() || i.Size() != a.Bytes {
		return fmt.Errorf("invalid artifact %s", path)
	}
	if !deep {
		return nil
	}
	got, e := fileHashContext(ctx, f)
	if e != nil || !strings.EqualFold(got, a.SHA256) {
		return fmt.Errorf("artifact hash mismatch %s", path)
	}
	return nil
}
func fileHashContext(ctx context.Context, p string) (string, error) {
	f, e := os.Open(p)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	buffer := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, readErr := f.Read(buffer)
		if n > 0 {
			if _, err := h.Write(buffer[:n]); err != nil {
				return "", err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return hex.EncodeToString(h.Sum(nil)), nil
		}
		if readErr != nil {
			return "", readErr
		}
	}
}
func free(path string) int64 {
	var s syscall.Statfs_t
	if syscall.Statfs(path, &s) != nil {
		return 0
	}
	return int64(s.Bavail) * int64(s.Bsize)
}
func (m *Manager) download(ctx context.Context, s Source, path string) error {
	if ok, err := validContext(ctx, path, s); err != nil {
		return failure("STORAGE", "Cached checkpoint could not be read", err)
	} else if ok {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return failure("STORAGE", "Checkpoint storage could not be prepared", err)
	}
	f, e := os.OpenFile(path+".part", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		return failure("STORAGE", "Checkpoint storage could not be prepared", e)
	}
	defer func() { f.Close(); os.Remove(path + ".part") }()
	r, e := http.NewRequestWithContext(ctx, "GET", s.URL, nil)
	if e != nil {
		return failure("NETWORK", "Checkpoint download request could not be created", e)
	}
	res, e := m.client.Do(r)
	if e != nil {
		return failure("NETWORK", "Checkpoint download failed", e)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return failure("UPSTREAM_STATUS", "Checkpoint server returned an unexpected status", fmt.Errorf("download status %d", res.StatusCode))
	}
	h := sha256.New()
	base := m.Latest().CompletedBytes
	last := time.Now()
	reader := &progressReader{Reader: io.LimitReader(res.Body, s.Bytes+1), update: func(n int64) {
		if time.Since(last) < 250*time.Millisecond {
			return
		}
		last = time.Now()
		m.mu.Lock()
		m.op.CompletedBytes = base + n
		if err := m.persist(); err != nil {
			m.logger.Error("persist download progress", "error", err)
		}
		m.mu.Unlock()
	}}
	n, e := io.Copy(io.MultiWriter(f, h), reader)
	if e != nil {
		return failure("NETWORK", "Checkpoint download failed", e)
	}
	if n != s.Bytes || hex.EncodeToString(h.Sum(nil)) != s.SHA256 {
		return failure("HASH", "Checkpoint size or hash did not match the approved source", errors.New("download size or hash mismatch"))
	}
	if e = f.Sync(); e != nil {
		return failure("STORAGE", "Checkpoint could not be stored", e)
	}
	if e = os.Rename(path+".part", path); e != nil {
		return failure("STORAGE", "Checkpoint could not be stored", e)
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return failure("STORAGE", "Checkpoint could not be stored", e)
	}
	defer d.Close()
	if e = d.Sync(); e != nil {
		return failure("STORAGE", "Checkpoint could not be stored", e)
	}
	return nil
}

type progressReader struct {
	io.Reader
	n      int64
	update func(int64)
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, e := r.Reader.Read(p)
	r.n += int64(n)
	if n > 0 {
		r.update(r.n)
	}
	return n, e
}
func validContext(ctx context.Context, p string, s Source) (bool, error) {
	i, e := os.Stat(p)
	if errors.Is(e, os.ErrNotExist) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if i.Size() != s.Bytes {
		return false, nil
	}
	f, e := os.Open(p)
	if e != nil {
		return false, e
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		n, err := f.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return false, err
		}
	}
	return hex.EncodeToString(h.Sum(nil)) == s.SHA256, nil
}
func secureClient() *http.Client {
	allowed := map[string]bool{"download.pytorch.org": true, "openaipublic.azureedge.net": true, "heibox.uni-heidelberg.de": true, "s3.amazonaws.com": true}
	d := &net.Dialer{Timeout: 10 * time.Second}
	tr := &http.Transport{Proxy: nil, DisableCompression: true, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, e := net.SplitHostPort(address)
		if e != nil || !allowed[host] {
			return nil, errors.New("download host not allowed")
		}
		ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if e != nil {
			return nil, e
		}
		for _, ip := range ips {
			if !publicIP(net.IP(ip.AsSlice())) {
				return nil, errors.New("download host resolved to non-public address")
			}
		}
		if len(ips) == 0 {
			return nil, errors.New("download host has no addresses")
		}
		_, port, _ := net.SplitHostPort(address)
		return dialValidated(ctx, d.DialContext, network, port, ips)
	}}
	return &http.Client{Transport: tr, Timeout: 30 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 3 || r.URL.Scheme != "https" || !allowed[r.URL.Hostname()] || len(via) == 0 || r.URL.Hostname() != via[0].URL.Hostname() {
			return errors.New("download redirect not allowed")
		}
		return nil
	}}
}
func dialValidated(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), network, port string, ips []netip.Addr) (net.Conn, error) {
	var last error
	for _, ip := range ips {
		conn, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	if last == nil {
		last = errors.New("download host has no addresses")
	}
	return nil, last
}
func publicIP(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	if a.Is4In6() {
		return false
	}
	if a.Is4() {
		v := a.As4()
		return !(v[0] == 0 || v[0] == 10 || v[0] == 100 && v[1] >= 64 && v[1] <= 127 || v[0] == 127 || v[0] >= 224 || v[0] == 169 && v[1] == 254 || v[0] == 172 && v[1] >= 16 && v[1] <= 31 || v[0] == 192 && (v[1] == 0 || v[1] == 168 || v[1] == 2 || v[1] == 88) || v[0] == 198 && (v[1] == 18 || v[1] == 19 || v[1] == 51) || v[0] == 203 && v[1] == 0 || v[0] >= 240)
	}
	if a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsMulticast() || a.IsUnspecified() || a.IsPrivate() || !a.IsGlobalUnicast() {
		return false
	}
	for _, prefix := range []netip.Prefix{netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2001:2::/48"), netip.MustParsePrefix("64:ff9b:1::/48")} {
		if prefix.Contains(a) {
			return false
		}
	}
	return true
}
