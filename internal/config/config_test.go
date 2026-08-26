package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerAddressFormatsIPv4AndIPv6(t *testing.T) {
	if got := (ServerConfig{Host: "127.0.0.1", Port: 8080}).Address(); got != "127.0.0.1:8080" {
		t.Fatalf("IPv4 address = %q", got)
	}
	if got := (ServerConfig{Host: "::1", Port: 8080}).Address(); got != "[::1]:8080" {
		t.Fatalf("IPv6 address = %q", got)
	}
}

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

func TestCheckpointDownloadsDefaultFalseAndStrictEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CheckpointDownloads.Enabled {
		t.Fatal("checkpoint downloads defaulted to enabled")
	}
	t.Setenv("UNCANNY_ENABLE_CHECKPOINT_DOWNLOADS", "true")
	cfg, err = Load(path)
	if err != nil || !cfg.CheckpointDownloads.Enabled {
		t.Fatalf("enabled config = %#v, %v", cfg.CheckpointDownloads, err)
	}
	t.Setenv("UNCANNY_ENABLE_CHECKPOINT_DOWNLOADS", "perhaps")
	if _, err := Load(path); err == nil {
		t.Fatal("invalid checkpoint environment value was accepted")
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	for name, content := range map[string]string{
		"obsolete UI library": "paths:\n  mewa_ui: /mewa-ui\n",
		"server typo":         "server:\n  adress: 127.0.0.1\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("Load succeeded with an unknown field")
			}
		})
	}
}
