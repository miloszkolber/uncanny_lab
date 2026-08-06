package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Status string

const (
	Queued       Status = "queued"
	Preparing    Status = "preparing"
	LoadingModel Status = "loading-model"
	Running      Status = "running"
	Saving       Status = "saving"
	Completed    Status = "completed"
	Failed       Status = "failed"
	Cancelled    Status = "cancelled"
)

type Job struct {
	ID               string          `json:"id"`
	Engine           string          `json:"engine"`
	Status           Status          `json:"status"`
	Parameters       json.RawMessage `json:"parameters"`
	Seed             int64           `json:"seed"`
	ProgressStep     int             `json:"progress_step"`
	ProgressTotal    int             `json:"progress_total"`
	PreviewPath      string          `json:"preview_path,omitempty"`
	FinalPath        string          `json:"final_path,omitempty"`
	ErrorCode        string          `json:"error_code,omitempty"`
	ErrorMessage     string          `json:"error_message,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	StartedAt        *time.Time      `json:"started_at,omitempty"`
	CompletedAt      *time.Time      `json:"completed_at,omitempty"`
	EngineVersion    string          `json:"engine_version"`
	RuntimeDevice    string          `json:"runtime_device"`
	RuntimePrecision string          `json:"runtime_precision"`
}

type CreateRequest struct {
	Engine     string          `json:"engine"`
	Parameters json.RawMessage `json:"parameters"`
	Seed       *int64          `json:"seed,omitempty"`
}

func NewID(now time.Time) (string, error) {
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate random ID: %w", err)
	}
	return fmt.Sprintf("%x-%s", now.UTC().UnixMilli(), hex.EncodeToString(random)), nil
}

func CanTransition(from, to Status) bool {
	allowed := map[Status][]Status{
		Queued:       {Preparing, Cancelled},
		Preparing:    {LoadingModel, Running, Failed, Cancelled},
		LoadingModel: {Running, Failed, Cancelled},
		Running:      {Saving, Completed, Failed, Cancelled},
		Saving:       {Completed, Failed, Cancelled},
	}
	for _, candidate := range allowed[from] {
		if candidate == to {
			return true
		}
	}
	return false
}

func ValidateCreate(req CreateRequest) error {
	if len(req.Parameters) == 0 {
		req.Parameters = json.RawMessage(`{}`)
	}
	var params map[string]any
	if err := json.Unmarshal(req.Parameters, &params); err != nil {
		return errors.New("parameters must be a JSON object")
	}
	if params == nil {
		return errors.New("parameters must be a JSON object")
	}
	return nil
}
