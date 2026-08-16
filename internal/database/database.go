package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/miloszkolber/uncanny-lab/internal/jobs"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("job not found")
var persistedJobID = regexp.MustCompile(`^[0-9a-f]+-[0-9a-f]{12}$`)

type Repository struct{ db *sql.DB }

func Open(path string) (*Repository, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping SQLite: %w", err)
	}
	r := &Repository{db: db}
	if err := r.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	for _, file := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(file, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			db.Close()
			return nil, fmt.Errorf("secure SQLite file: %w", err)
		}
	}
	return r, nil
}

func (r *Repository) Close() error { return r.db.Close() }

// Reconcile restores terminal jobs from their durable metadata when the SQLite index is replaced.
func (r *Repository) Reconcile(ctx context.Context, root string) (int, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		entries = nil
		err = nil
	}
	if err != nil {
		return 0, fmt.Errorf("read job artifacts: %w", err)
	}
	restored := 0
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !persistedJobID.MatchString(entry.Name()) {
			continue
		}
		metadataPath := filepath.Join(root, entry.Name(), "metadata.json")
		info, err := os.Lstat(metadataPath)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		file, err := os.Open(metadataPath)
		if err != nil {
			continue
		}
		var metadata struct {
			Job jobs.Job `json:"job"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(file, 2<<20)).Decode(&metadata)
		closeErr := file.Close()
		job := metadata.Job
		terminal := job.Status == jobs.Completed || job.Status == jobs.Failed || job.Status == jobs.Cancelled
		if decodeErr != nil || closeErr != nil || job.ID != entry.Name() || !terminal || job.CreatedAt.IsZero() {
			continue
		}
		if _, err := r.Get(ctx, entry.Name()); errors.Is(err, ErrNotFound) {
			if err := r.Create(ctx, job); err != nil {
				return restored, fmt.Errorf("restore job %s: %w", job.ID, err)
			}
			restored++
		} else if err != nil {
			return restored, err
		} else if err := r.Save(ctx, job); err != nil {
			return restored, fmt.Errorf("reconcile job %s: %w", job.ID, err)
		}
	}
	if err := r.backfillTerminalMetadata(ctx, root); err != nil {
		return restored, err
	}
	return restored, nil
}

func (r *Repository) backfillTerminalMetadata(ctx context.Context, root string) error {
	rows, err := r.db.QueryContext(ctx, `SELECT id, engine, status, parameters, seed, progress_step, progress_total, preview_path, final_path, error_code, error_message, created_at, started_at, completed_at, engine_version, runtime_device, runtime_precision FROM jobs WHERE status IN (?, ?, ?) ORDER BY created_at ASC`, jobs.Completed, jobs.Failed, jobs.Cancelled)
	if err != nil {
		return fmt.Errorf("list terminal jobs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return fmt.Errorf("scan terminal jobs: %w", err)
		}
		directory := filepath.Join(root, job.ID)
		path := filepath.Join(directory, "metadata.json")
		if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
			continue
		}
		if err := os.MkdirAll(directory, 0o750); err != nil {
			return fmt.Errorf("create terminal artifact directory: %w", err)
		}
		data, err := json.MarshalIndent(map[string]any{"application": "Uncanny Lab", "terminal_at": job.CompletedAt, "job": job}, "", "  ")
		if err != nil {
			return err
		}
		temporary := path + ".tmp"
		if err := os.WriteFile(temporary, append(data, '\n'), 0o640); err != nil {
			return fmt.Errorf("write terminal metadata: %w", err)
		}
		if err := os.Rename(temporary, path); err != nil {
			_ = os.Remove(temporary)
			return fmt.Errorf("publish terminal metadata: %w", err)
		}
	}
	return rows.Err()
}

func (r *Repository) migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS jobs (
    id TEXT PRIMARY KEY,
    engine TEXT NOT NULL,
    status TEXT NOT NULL,
    parameters TEXT NOT NULL,
    seed INTEGER NOT NULL,
    progress_step INTEGER NOT NULL DEFAULT 0,
    progress_total INTEGER NOT NULL DEFAULT 0,
    preview_path TEXT NOT NULL DEFAULT '',
    final_path TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    started_at TEXT,
    completed_at TEXT,
    engine_version TEXT NOT NULL,
    runtime_device TEXT NOT NULL,
    runtime_precision TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS jobs_created_at ON jobs(created_at DESC);
CREATE INDEX IF NOT EXISTS jobs_status ON jobs(status);
`
	if _, err := r.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	return nil
}

func (r *Repository) Create(ctx context.Context, job jobs.Job) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO jobs
(id, engine, status, parameters, seed, progress_step, progress_total, preview_path, final_path, error_code, error_message, created_at, started_at, completed_at, engine_version, runtime_device, runtime_precision)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, jobFields(job)...)
	if err != nil {
		return fmt.Errorf("insert job %s: %w", job.ID, err)
	}
	return nil
}

func (r *Repository) Save(ctx context.Context, job jobs.Job) error {
	fields := jobFields(job)
	args := append(fields[1:], job.ID)
	result, err := r.db.ExecContext(ctx, `UPDATE jobs SET
engine=?, status=?, parameters=?, seed=?, progress_step=?, progress_total=?, preview_path=?, final_path=?, error_code=?, error_message=?, created_at=?, started_at=?, completed_at=?, engine_version=?, runtime_device=?, runtime_precision=? WHERE id=?`, args...)
	if err != nil {
		return fmt.Errorf("update job %s: %w", job.ID, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count updated jobs: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) Get(ctx context.Context, id string) (jobs.Job, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id, engine, status, parameters, seed, progress_step, progress_total, preview_path, final_path, error_code, error_message, created_at, started_at, completed_at, engine_version, runtime_device, runtime_precision FROM jobs WHERE id=?`, id)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return jobs.Job{}, ErrNotFound
	}
	if err != nil {
		return jobs.Job{}, fmt.Errorf("read job %s: %w", id, err)
	}
	return job, nil
}

func (r *Repository) List(ctx context.Context, limit, offset int) ([]jobs.Job, error) {
	if limit < 1 || limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, engine, status, parameters, seed, progress_step, progress_total, preview_path, final_path, error_code, error_message, created_at, started_at, completed_at, engine_version, runtime_device, runtime_precision FROM jobs ORDER BY created_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()
	result := make([]jobs.Job, 0)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan jobs: %w", err)
		}
		result = append(result, job)
	}
	return result, rows.Err()
}

func (r *Repository) ListRecoverable(ctx context.Context) ([]jobs.Job, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, engine, status, parameters, seed, progress_step, progress_total, preview_path, final_path, error_code, error_message, created_at, started_at, completed_at, engine_version, runtime_device, runtime_precision FROM jobs WHERE status IN (?, ?, ?, ?, ?) ORDER BY created_at ASC`, jobs.Queued, jobs.Preparing, jobs.LoadingModel, jobs.Running, jobs.Saving)
	if err != nil {
		return nil, fmt.Errorf("list recoverable jobs: %w", err)
	}
	defer rows.Close()
	var result []jobs.Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan recoverable jobs: %w", err)
		}
		result = append(result, job)
	}
	return result, rows.Err()
}

func (r *Repository) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM jobs WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete job %s: %w", id, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count deleted jobs: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func jobFields(job jobs.Job) []any {
	return []any{job.ID, job.Engine, job.Status, string(job.Parameters), job.Seed, job.ProgressStep, job.ProgressTotal, job.PreviewPath, job.FinalPath, job.ErrorCode, job.ErrorMessage, formatTime(job.CreatedAt), formatOptionalTime(job.StartedAt), formatOptionalTime(job.CompletedAt), job.EngineVersion, job.RuntimeDevice, job.RuntimePrecision}
}

type scanner interface{ Scan(...any) error }

func scanJob(s scanner) (jobs.Job, error) {
	var job jobs.Job
	var parameters, created string
	var started, completed sql.NullString
	err := s.Scan(&job.ID, &job.Engine, &job.Status, &parameters, &job.Seed, &job.ProgressStep, &job.ProgressTotal, &job.PreviewPath, &job.FinalPath, &job.ErrorCode, &job.ErrorMessage, &created, &started, &completed, &job.EngineVersion, &job.RuntimeDevice, &job.RuntimePrecision)
	if err != nil {
		return jobs.Job{}, err
	}
	job.Parameters = json.RawMessage(parameters)
	job.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return jobs.Job{}, err
	}
	job.StartedAt, err = parseOptionalTime(started)
	if err != nil {
		return jobs.Job{}, err
	}
	job.CompletedAt, err = parseOptionalTime(completed)
	return job, err
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }
func formatOptionalTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}
func parseOptionalTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	return &parsed, err
}
