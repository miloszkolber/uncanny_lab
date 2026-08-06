package engines

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var modelIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

type ModelDescriptor struct {
	ID      string   `json:"id"`
	Path    string   `json:"path"`
	SHA256  string   `json:"sha256,omitempty"`
	Family  string   `json:"family,omitempty"`
	Engines []string `json:"engines,omitempty"`
	License string   `json:"license,omitempty"`
	Notes   string   `json:"notes,omitempty"`
}

type ModelStatus struct {
	ModelDescriptor
	Status string `json:"status"`
	Hash   string `json:"hash,omitempty"`
}

func LoadModels(root string, registry *Registry) ([]ModelStatus, error) {
	registryRoot := filepath.Join(root, "registry")
	entries, err := os.ReadDir(registryRoot)
	if errors.Is(err, os.ErrNotExist) {
		return requiredModels(registry), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read model registry: %w", err)
	}
	models := make(map[string]ModelStatus)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(registryRoot, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read model descriptor %s: %w", entry.Name(), err)
		}
		var descriptor ModelDescriptor
		if err := json.Unmarshal(data, &descriptor); err != nil {
			return nil, fmt.Errorf("decode model descriptor %s: %w", entry.Name(), err)
		}
		if !modelIDPattern.MatchString(descriptor.ID) || descriptor.Path == "" {
			return nil, fmt.Errorf("invalid model descriptor %s", entry.Name())
		}
		if _, found := models[descriptor.ID]; found {
			return nil, fmt.Errorf("duplicate model ID %s", descriptor.ID)
		}
		models[descriptor.ID] = statusFor(root, descriptor)
	}
	for _, required := range requiredModels(registry) {
		if _, found := models[required.ID]; !found {
			models[required.ID] = required
		}
	}
	result := make([]ModelStatus, 0, len(models))
	for _, model := range models {
		result = append(result, model)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func requiredModels(registry *Registry) []ModelStatus {
	result := map[string]ModelStatus{}
	for _, engine := range registry.All() {
		for _, id := range engine.Models {
			if modelIDPattern.MatchString(id) {
				model := result[id]
				model.ID, model.Status = id, "missing"
				model.Engines = append(model.Engines, engine.ID)
				result[id] = model
			}
		}
	}
	models := make([]ModelStatus, 0, len(result))
	for _, model := range result {
		models = append(models, model)
	}
	return models
}

func statusFor(root string, descriptor ModelDescriptor) ModelStatus {
	status := ModelStatus{ModelDescriptor: descriptor, Status: "missing"}
	path, err := containedFile(root, descriptor.Path)
	if errors.Is(err, os.ErrNotExist) {
		return status
	}
	if err != nil {
		status.Status = "invalid"
		return status
	}
	status.Path = path
	if _, err := os.Stat(path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			status.Status = "invalid"
		}
		return status
	}
	hash, err := FileSHA256(path)
	if err != nil {
		status.Status = "invalid"
		return status
	}
	status.Hash = hash
	if descriptor.SHA256 != "" && !strings.EqualFold(descriptor.SHA256, hash) {
		status.Status = "invalid"
		return status
	}
	status.Status = "available"
	return status
}

func VerifyModel(root, id string, registry *Registry) (ModelStatus, error) {
	if !modelIDPattern.MatchString(id) {
		return ModelStatus{}, errors.New("invalid model ID")
	}
	models, err := LoadModels(root, registry)
	if err != nil {
		return ModelStatus{}, err
	}
	for _, model := range models {
		if model.ID == id {
			return model, nil
		}
	}
	return ModelStatus{}, os.ErrNotExist
}

func containedFile(root, value string) (string, error) {
	if filepath.IsAbs(value) || filepath.Clean(value) == "." || filepath.Clean(value) == ".." || strings.HasPrefix(filepath.Clean(value), ".."+string(filepath.Separator)) {
		return "", errors.New("absolute model path")
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, filepath.Clean(value))
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if resolved == root || !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return "", errors.New("model path escapes root")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("model is not a regular file")
	}
	return resolved, nil
}

func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
