package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig  `yaml:"server"`
	Runtime  RuntimeConfig `yaml:"runtime"`
	Paths    PathsConfig   `yaml:"paths"`
	Previews PreviewConfig `yaml:"previews"`
}

type ServerConfig struct {
	Host string `yaml:"address"`
	Port int    `yaml:"port"`
}

func (s ServerConfig) Address() string { return fmt.Sprintf("%s:%d", s.Host, s.Port) }

type RuntimeConfig struct {
	Device           string `yaml:"device"`
	DefaultPrecision string `yaml:"default_precision"`
	PythonExecutable string `yaml:"python_executable"`
	PythonPath       string `yaml:"python_path"`
}

type PathsConfig struct {
	Models    string `yaml:"models"`
	Inputs    string `yaml:"inputs"`
	Outputs   string `yaml:"outputs"`
	Workspace string `yaml:"workspace"`
	Manifests string `yaml:"manifests"`
}

type PreviewConfig struct {
	Enabled    bool `yaml:"enabled"`
	EverySteps int  `yaml:"every_steps"`
}

func defaults() Config {
	return Config{
		Server:   ServerConfig{Host: "0.0.0.0", Port: 8080},
		Runtime:  RuntimeConfig{Device: "xpu", DefaultPrecision: "fp32", PythonExecutable: "python3", PythonPath: "/app/python"},
		Paths:    PathsConfig{Models: "/models", Inputs: "/inputs", Outputs: "/outputs", Workspace: "/workspace", Manifests: "/app/manifests/engines"},
		Previews: PreviewConfig{Enabled: true, EverySteps: 5},
	}
}

func Load(path string) (Config, error) {
	cfg := defaults()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, cfg.Validate()
	}
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if c.Server.Host == "" || c.Server.Port < 1 || c.Server.Port > 65535 {
		return errors.New("server address and port must be valid")
	}
	if c.Runtime.Device != "xpu" && c.Runtime.Device != "cpu" {
		return errors.New("runtime.device must be xpu or cpu")
	}
	if c.Runtime.DefaultPrecision != "fp32" && c.Runtime.DefaultPrecision != "fp16" {
		return errors.New("runtime.default_precision must be fp32 or fp16")
	}
	if c.Runtime.PythonExecutable == "" || c.Runtime.PythonPath == "" {
		return errors.New("runtime Python executable and path are required")
	}
	for name, path := range map[string]string{"models": c.Paths.Models, "inputs": c.Paths.Inputs, "outputs": c.Paths.Outputs, "workspace": c.Paths.Workspace} {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("paths.%s must be absolute", name)
		}
	}
	if !filepath.IsAbs(c.Paths.Manifests) {
		return errors.New("paths.manifests must be absolute")
	}
	if c.Previews.EverySteps < 1 {
		return errors.New("previews.every_steps must be positive")
	}
	return nil
}

func (c Config) EnsureDirectories() error {
	for _, path := range []string{c.Paths.Models, c.Paths.Inputs, c.Paths.Outputs, c.Paths.Workspace, filepath.Join(c.Paths.Workspace, "jobs")} {
		if err := os.MkdirAll(path, 0o750); err != nil {
			return fmt.Errorf("create %s: %w", path, err)
		}
	}
	return nil
}

func (c Config) DatabasePath() string { return filepath.Join(c.Paths.Workspace, "legacy-lab.db") }
func (c Config) JobRoot() string      { return filepath.Join(c.Paths.Workspace, "jobs") }
