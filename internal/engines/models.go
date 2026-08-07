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
var sha256Pattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

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
		return requiredModels(root, registry), nil
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
		models[descriptor.ID] = statusFor(root, descriptor, descriptor.SHA256 != "")
	}
	for _, required := range requiredModels(root, registry) {
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

func requiredModels(root string, registry *Registry) []ModelStatus {
	result := map[string]ModelStatus{}
	for _, engine := range registry.Enabled() {
		for _, id := range engine.Models {
			if modelIDPattern.MatchString(id) {
				model := result[id]
				if model.ID == "" {
					model.ModelDescriptor = builtinModel(id)
				}
				model.ID, model.Status = id, "missing"
				model.Engines = append(model.Engines, engine.ID)
				if model.Path != "" {
					model = statusFor(root, model.ModelDescriptor, false)
				}
				result[id] = model
			}
		}
	}
	models := make([]ModelStatus, 0, len(result))
	for _, model := range result {
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models
}

func statusFor(root string, descriptor ModelDescriptor, verify bool) ModelStatus {
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
	if descriptor.SHA256 == "" && strings.HasPrefix(descriptor.Path, "bundle-b/") {
		descriptor.SHA256 = bundleArtifactHash(root, descriptor.Path)
		status.ModelDescriptor = descriptor
		if !sha256Pattern.MatchString(descriptor.SHA256) {
			status.Status = "invalid"
			return status
		}
	}
	if verify {
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
	}
	status.Status = "available"
	return status
}

// bundleArtifactHash makes built-in Bundle B descriptors verifiable without duplicating
// conversion hashes in manifests. The signed-by-hash provenance is itself checked by
// modelinstall before publication and is rechecked against each artifact here.
func bundleArtifactHash(root, path string) string {
	report, err := containedFile(root, "bundle-b/provenance/bundle-b-conversion-report.json")
	if err != nil {
		return ""
	}
	var value struct {
		Format string `json:"format"`
		VGG19  struct {
			Artifact struct {
				SHA256 string `json:"sha256"`
			} `json:"artifact"`
		} `json:"vgg19"`
		Clip struct {
			Artifact struct {
				SHA256 string `json:"sha256"`
			} `json:"artifact"`
		} `json:"clip"`
		VQGAN struct {
			Decoder struct {
				SHA256 string `json:"sha256"`
			} `json:"decoder"`
			Codebook struct {
				SHA256 string `json:"sha256"`
			} `json:"codebook"`
		} `json:"vqgan"`
		BigGAN struct {
			Artifact struct {
				SHA256 string `json:"sha256"`
			} `json:"artifact"`
		} `json:"biggan"`
	}
	if json.Unmarshal(mustRead(report), &value) != nil || value.Format != "uncanny-lab-bundle-b-conversion-v2" {
		return ""
	}
	switch path {
	case "bundle-b/classifiers/vgg19.pt":
		return value.VGG19.Artifact.SHA256
	case "bundle-b/clip/vit-b-32.pt":
		return value.Clip.Artifact.SHA256
	case "bundle-b/vqgan/decoder.pt":
		return value.VQGAN.Decoder.SHA256
	case "bundle-b/vqgan/codebook.pt":
		return value.VQGAN.Codebook.SHA256
	case "bundle-b/biggan/generator.pt":
		return value.BigGAN.Artifact.SHA256
	}
	return ""
}
func mustRead(path string) []byte { b, _ := os.ReadFile(path); return b }

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
			if model.Path == "" {
				return model, nil
			}
			descriptor := model.ModelDescriptor
			if filepath.IsAbs(descriptor.Path) {
				relative, relativeErr := filepath.Rel(root, descriptor.Path)
				if relativeErr != nil {
					return ModelStatus{}, relativeErr
				}
				descriptor.Path = relative
			}
			return statusFor(root, descriptor, true), nil
		}
	}
	return ModelStatus{}, os.ErrNotExist
}

func builtinModel(id string) ModelDescriptor {
	switch id {
	case "vgg19-imagenet":
		return ModelDescriptor{ID: id, Path: "bundle-b/classifiers/vgg19.pt", Family: "Classifiers", License: "Checkpoint license applies", Notes: "TorchVision-compatible VGG19 state_dict"}
	case "clip-vit-b-32":
		return ModelDescriptor{ID: id, Path: "bundle-b/clip/vit-b-32.pt", Family: "CLIP", License: "Checkpoint license applies", Notes: "OpenCLIP ViT-B-32 state_dict"}
	case "vqgan-decoder":
		return ModelDescriptor{ID: id, Path: "bundle-b/vqgan/decoder.pt", Family: "VQGAN", License: "Checkpoint license applies", Notes: "TorchScript decoder accepting a quantized BCHW embedding grid"}
	case "vqgan-codebook":
		return ModelDescriptor{ID: id, Path: "bundle-b/vqgan/codebook.pt", Family: "VQGAN", License: "Checkpoint license applies", Notes: "Weights-only state dictionary containing the VQ embedding matrix"}
	case "biggan-generator":
		return ModelDescriptor{ID: id, Path: "bundle-b/biggan/generator.pt", Family: "BigGAN", License: "Checkpoint license applies", Notes: "TorchScript class-conditioned generator accepting latent and class-probability tensors"}
	default:
		return ModelDescriptor{ID: id}
	}
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
