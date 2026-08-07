package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllowedHostsMergesLoopbackAndConfiguredHosts(t *testing.T) {
	hosts, err := AllowedHosts(9090, " Lab.Example.Test:9090 , api.example.test:9090 ")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(hosts, ",")
	for _, expected := range []string{"localhost:9090", "127.0.0.1:9090", "[::1]:9090", "lab.example.test:9090", "api.example.test:9090"} {
		if !strings.Contains(got, expected) {
			t.Errorf("allowlist %q does not contain %q", got, expected)
		}
	}
}

func TestAllowedHostsRejectsEmptyAndWildcardEntries(t *testing.T) {
	for _, value := range []string{"example.test:8080,", "example.test:8080, ,other.test:8080", "*.example.test:8080"} {
		if _, err := AllowedHosts(8080, value); err == nil {
			t.Errorf("AllowedHosts(%q) succeeded", value)
		}
	}
}

func TestLoadUsesConfiguredPortAndHosts(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("server:\n  address: 0.0.0.0\n  port: 8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UNCANNY_PORT", "9090")
	t.Setenv("UNCANNY_ALLOWED_HOSTS", " Lab.Example.Test:9090 ")
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 9090 {
		t.Fatalf("port = %d", cfg.Server.Port)
	}
	if got := strings.Join(cfg.Server.AllowedHosts, ","); !strings.Contains(got, "lab.example.test:9090") || !strings.Contains(got, "localhost:9090") {
		t.Fatalf("unexpected allowlist %q", got)
	}
}

func TestLoadUsesConfiguredDevice(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("runtime:\n  device: xpu\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UNCANNY_DEVICE", " CPU ")
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.Device != "cpu" {
		t.Fatalf("device = %q, want cpu", cfg.Runtime.Device)
	}
}

func TestLoadRejectsInvalidConfiguredDevice(t *testing.T) {
	t.Setenv("UNCANNY_DEVICE", "cuda")
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("Load succeeded with an unsupported UNCANNY_DEVICE")
	}
}
