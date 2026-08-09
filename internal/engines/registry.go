package engines

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Manifest struct {
	ID             string                 `yaml:"id" json:"id"`
	Name           string                 `yaml:"name" json:"name"`
	Type           string                 `yaml:"type" json:"type"`
	Version        string                 `yaml:"version" json:"version"`
	Description    string                 `yaml:"description" json:"description"`
	Capabilities   map[string]bool        `yaml:"capabilities" json:"capabilities"`
	RequiredInputs []string               `yaml:"required_inputs,omitempty" json:"required_inputs,omitempty"`
	Runtime        map[string]any         `yaml:"runtime" json:"runtime"`
	Parameters     map[string]interface{} `yaml:"parameters" json:"parameters"`
	Enabled        *bool                  `yaml:"enabled" json:"enabled"`
	Models         []string               `yaml:"models" json:"models"`
}

type Registry struct{ manifests []Manifest }

func Load(directory string) (*Registry, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read engine manifests: %w", err)
	}
	var manifests []Manifest
	seen := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() || (filepath.Ext(entry.Name()) != ".yaml" && filepath.Ext(entry.Name()) != ".yml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read manifest %s: %w", entry.Name(), err)
		}
		var manifest Manifest
		if err := yaml.Unmarshal(data, &manifest); err != nil {
			return nil, fmt.Errorf("decode manifest %s: %w", entry.Name(), err)
		}
		if manifest.ID == "" || manifest.Name == "" || manifest.Version == "" || !strings.Contains(manifest.Type, "-to-") {
			return nil, fmt.Errorf("manifest %s is missing required fields", entry.Name())
		}
		if seen[manifest.ID] {
			return nil, fmt.Errorf("duplicate engine ID %s", manifest.ID)
		}
		seen[manifest.ID] = true
		manifests = append(manifests, manifest)
	}
	if len(manifests) == 0 {
		return nil, fmt.Errorf("no engine manifests found in %s", directory)
	}
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].Name < manifests[j].Name })
	return &Registry{manifests: manifests}, nil
}

func (r *Registry) All() []Manifest { return append([]Manifest(nil), r.manifests...) }

func (r *Registry) Enabled() []Manifest {
	result := make([]Manifest, 0, len(r.manifests))
	for _, manifest := range r.manifests {
		if manifest.IsEnabled() {
			result = append(result, manifest)
		}
	}
	return result
}

func (r *Registry) Get(id string) (Manifest, bool) {
	for _, manifest := range r.manifests {
		if manifest.ID == id {
			return manifest, true
		}
	}
	return Manifest{}, false
}

func (m Manifest) IsEnabled() bool { return m.Enabled == nil || *m.Enabled }

func (m Manifest) ApplyDefaults(parameters json.RawMessage) (json.RawMessage, error) {
	values := make(map[string]any)
	if len(parameters) > 0 {
		if err := json.Unmarshal(parameters, &values); err != nil {
			return nil, fmt.Errorf("decode parameters: %w", err)
		}
	}
	for name, raw := range m.Parameters {
		schema, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if _, supplied := values[name]; supplied {
			continue
		}
		if value, exists := schema["default"]; exists {
			values[name] = value
		}
	}
	normalized, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("encode parameters: %w", err)
	}
	return normalized, nil
}
