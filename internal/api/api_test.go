package api

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/uncanny-lab/internal/config"
	"github.com/miloszkolber/uncanny-lab/internal/database"
	"github.com/miloszkolber/uncanny-lab/internal/jobs"
)

func TestUploadRejectsWrongContentType(t *testing.T) {
	a := &API{cfg: config.Config{Paths: config.PathsConfig{Inputs: t.TempDir()}}}
	req := httptest.NewRequest(http.MethodPost, "/api/uploads", bytes.NewBufferString("not multipart"))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	a.upload(response, req)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestUploadAcceptsPNGAndNormalizesToken(t *testing.T) {
	inputs := t.TempDir()
	a := &API{cfg: config.Config{Paths: config.PathsConfig{Inputs: inputs}}}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("image", "untrusted.jpg")
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.Black)
	if err := png.Encode(part, img); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/uploads", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	a.upload(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var result map[string]string
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !uploadIDPattern.MatchString(result["token"][len("inputs/") : len(result["token"])-len(".png")]) {
		t.Fatalf("unsafe token %q", result["token"])
	}
}

func TestUploadRejectsOversizedDimensions(t *testing.T) {
	a := &API{cfg: config.Config{Paths: config.PathsConfig{Inputs: t.TempDir()}}}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("image", "large.png")
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(part, image.NewRGBA(image.Rect(0, 0, maxImageDimension+1, 1))); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/uploads", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	a.upload(response, req)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestDeleteRejectsSymlinkedJobDirectory(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, "jobs"), 0o750); err != nil {
		t.Fatal(err)
	}
	repo, err := database.Open(filepath.Join(root, "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	id := "123-0123456789ab"
	job := jobs.Job{ID: id, Engine: "test", Status: jobs.Completed, Parameters: json.RawMessage(`{}`), CreatedAt: time.Now().UTC()}
	if err := repo.Create(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "jobs", id)); err != nil {
		t.Fatal(err)
	}
	a := &API{repo: repo, cfg: config.Config{Paths: config.PathsConfig{Workspace: workspace}}}
	req := httptest.NewRequest(http.MethodDelete, "/api/jobs/"+id, nil)
	req.SetPathValue("id", id)
	response := httptest.NewRecorder()
	a.deleteJob(response, req)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("outside directory was removed")
	}
}

func TestDuplicatePreservesPrivateInputAfterOriginalDeletion(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	inputs := filepath.Join(root, "inputs")
	id := "123-0123456789ab"
	privateInput := filepath.Join(workspace, "jobs", id, "inputs", "source_image.png")
	if err := os.MkdirAll(filepath.Dir(privateInput), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(inputs, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privateInput, []byte("image"), 0o640); err != nil {
		t.Fatal(err)
	}
	a := &API{cfg: config.Config{Paths: config.PathsConfig{Workspace: workspace, Inputs: inputs}}}
	original := jobs.Job{ID: id, Parameters: json.RawMessage(`{"source_image":"workspace/jobs/upstream/final.png"}`)}
	parameters, err := a.duplicateParameters(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(workspace, "jobs", id)); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(parameters, &decoded); err != nil {
		t.Fatal(err)
	}
	token := decoded["source_image"]
	if !strings.HasPrefix(token, "inputs/") {
		t.Fatalf("duplicate input was not made durable: %q", token)
	}
	if _, err := os.Stat(filepath.Join(root, token)); err != nil {
		t.Fatalf("durable duplicate input is missing: %v", err)
	}
}
