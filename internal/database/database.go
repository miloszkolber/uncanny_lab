package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/miloszkolber/legacy-image-lab/internal/jobs"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("job not found")

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
	return r, nil
}

func (r *Repository) Close() error { return r.db.Close() }

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

func (r *Repository) List(ctx context.Context, limit int) ([]jobs.Job, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, engine, status, parameters, seed, progress_step, progress_total, preview_path, final_path, error_code, error_message, created_at, started_at, completed_at, engine_version, runtime_device, runtime_precision FROM jobs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()
	var result []jobs.Job
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
