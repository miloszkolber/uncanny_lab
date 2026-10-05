package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHealthcheckSamplesSelectedPackage(t *testing.T) {
	const foundation = "/ui/mewa-ui/510d5083135db0edd06415d36b2d9096b968e354aa86acba9c132f90ad8f4fdd/css/base.css"
	for _, test := range []struct {
		name              string
		healthOK, assetOK bool
		want              int
	}{
		{"healthy selected package", true, true, 0},
		{"missing selected asset", true, false, 1},
		{"unhealthy application", false, true, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/healthz" && test.healthOK || r.URL.Path == foundation && test.assetOK {
					w.WriteHeader(http.StatusOK)
					return
				}
				http.NotFound(w, r)
			}))
			defer server.Close()
			_, port, err := net.SplitHostPort(server.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("UNCANNY_PORT", port)
			if got := healthcheck(); got != test.want {
				t.Fatalf("healthcheck = %d, want %d", got, test.want)
			}
		})
	}
}

// The probed foundation must be the package the authored page loads and that is
// actually imported; a rollback-only package must not be probed.
func TestHealthcheckFoundationIsTheImportedPackage(t *testing.T) {
	const foundation = "/ui/mewa-ui/510d5083135db0edd06415d36b2d9096b968e354aa86acba9c132f90ad8f4fdd/css/base.css"
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "ui", strings.TrimPrefix(foundation, "/ui/"))); err != nil {
		t.Fatalf("probed foundation is not imported: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "web", "static", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"`+foundation+`"`) {
		t.Errorf("index does not reference the probed foundation %s", foundation)
	}
}

func TestHealthcheckFailsAgainstClosedPort(t *testing.T) {
	t.Setenv("UNCANNY_PORT", "9")
	if got := healthcheck(); got == 0 {
		t.Error("healthcheck() = 0 against a closed port, want nonzero")
	}
}

func TestListenNetworkMatchesLiteralAddressFamily(t *testing.T) {
	for host, expected := range map[string]string{
		"0.0.0.0":   "tcp4",
		"127.0.0.1": "tcp4",
		"::":        "tcp6",
		"::1":       "tcp6",
		"localhost": "tcp",
	} {
		if actual := listenNetwork(host); actual != expected {
			t.Errorf("listenNetwork(%q) = %q, want %q", host, actual, expected)
		}
	}
}
