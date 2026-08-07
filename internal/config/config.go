package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig  `yaml:"server"`
	Runtime  RuntimeConfig `yaml:"runtime"`
	Paths    PathsConfig   `yaml:"paths"`
	Previews PreviewConfig `yaml:"previews"`
}

type ServerConfig struct {
	Host         string   `yaml:"address"`
	Port         int      `yaml:"port"`
	AllowedHosts []string `yaml:"-"`
}

func (s ServerConfig) Address() string { return fmt.Sprintf("%s:%d", s.Host, s.Port) }

type RuntimeConfig struct {
	Device           string `yaml:"device"`
	DefaultPrecision string `yaml:"default_precision"`
	PythonExecutable string `yaml:"python_executable"`
	PythonPath       string `yaml:"python_path"`
}

type PathsConfig struct {
	Data      string `yaml:"data"`
	Models    string `yaml:"models"`
	Inputs    string `yaml:"inputs"`
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
		Runtime:  RuntimeConfig{Device: "xpu", DefaultPrecision: "fp32", PythonExecutable: "python3", PythonPath: "/opt/venv/lib/python3.12/site-packages"},
		Paths:    PathsConfig{Data: "/data", Models: "/data/models", Inputs: "/data/inputs", Workspace: "/data/workspace", Manifests: "/app/manifests/engines"},
		Previews: PreviewConfig{Enabled: true, EverySteps: 5},
	}
}

func Load(path string) (Config, error) {
	cfg := defaults()
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	if err == nil {
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(&cfg); err != nil {
			return Config{}, fmt.Errorf("decode %s: %w", path, err)
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return Config{}, fmt.Errorf("decode %s: multiple YAML documents are not supported", path)
		}
	}
	if value, ok := os.LookupEnv("UNCANNY_PORT"); ok {
		port, err := strconv.Atoi(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse UNCANNY_PORT: %w", err)
		}
		cfg.Server.Port = port
	}
	if value, ok := os.LookupEnv("UNCANNY_DEVICE"); ok {
		cfg.Runtime.Device = strings.ToLower(strings.TrimSpace(value))
	}
	allowedHostsValue, allowedHostsConfigured := os.LookupEnv("UNCANNY_ALLOWED_HOSTS")
	if allowedHostsConfigured && strings.TrimSpace(allowedHostsValue) == "" {
		return Config{}, errors.New("UNCANNY_ALLOWED_HOSTS must not be empty when configured")
	}
	allowedHosts, err := AllowedHosts(cfg.Server.Port, allowedHostsValue)
	if err != nil {
		return Config{}, err
	}
	cfg.Server.AllowedHosts = allowedHosts
	return cfg, cfg.Validate()
}

// AllowedHosts returns the Host header allowlist for a server port. Additional
// entries must be complete Host header values, including a non-default port.
func AllowedHosts(port int, value string) ([]string, error) {
	if port < 1 || port > 65535 {
		return nil, errors.New("server port must be valid")
	}
	allowed := map[string]struct{}{
		fmt.Sprintf("localhost:%d", port): {},
		fmt.Sprintf("127.0.0.1:%d", port): {},
		fmt.Sprintf("[::1]:%d", port):     {},
	}
	if value == "" {
		return hostList(allowed), nil
	}
	for _, entry := range strings.Split(value, ",") {
		host := strings.ToLower(strings.TrimSpace(entry))
		if host == "" {
			return nil, errors.New("UNCANNY_ALLOWED_HOSTS must not contain empty entries")
		}
		if strings.Contains(host, "*") {
			return nil, errors.New("UNCANNY_ALLOWED_HOSTS must not contain wildcard entries")
		}
		allowed[host] = struct{}{}
	}
	return hostList(allowed), nil
}

func hostList(hosts map[string]struct{}) []string {
	values := make([]string, 0, len(hosts))
	for host := range hosts {
		values = append(values, host)
	}
	sort.Strings(values)
	return values
}

func (c Config) Validate() error {
	if c.Server.Host == "" || c.Server.Port < 1 || c.Server.Port > 65535 {
		return errors.New("server address and port must be valid")
	}
	if len(c.Server.AllowedHosts) == 0 {
		return errors.New("server allowed hosts are required")
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
	for name, path := range map[string]string{"data": c.Paths.Data, "models": c.Paths.Models, "inputs": c.Paths.Inputs, "workspace": c.Paths.Workspace} {
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
	for _, path := range []string{c.Paths.Data, c.Paths.Models, c.Paths.Inputs, c.Paths.Workspace, filepath.Join(c.Paths.Workspace, "jobs")} {
		if path == "" {
			continue
		}
		if err := os.MkdirAll(path, 0o750); err != nil {
			return fmt.Errorf("create %s: %w", path, err)
		}
		if err := os.Chmod(path, 0o750); err != nil {
			return fmt.Errorf("secure %s: %w", path, err)
		}
	}
	return nil
}

func (c Config) DatabasePath() string { return filepath.Join(c.Paths.Workspace, "uncanny-lab.db") }
func (c Config) JobRoot() string      { return filepath.Join(c.Paths.Workspace, "jobs") }
