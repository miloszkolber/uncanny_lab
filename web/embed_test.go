package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestEmbeddedUIAssetsAreSelfContained(t *testing.T) {
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	for path, expected := range map[string]string{
		"/styles.css":  "/* Core UI foundation",
		"/lucide.svg":  `<symbol id="sparkles"`,
		"/favicon.svg": `<svg xmlns="http://www.w3.org/2000/svg"`,
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, response.Code)
		}
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), expected) {
			t.Errorf("GET %s does not contain %q", path, expected)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/lucide.svg", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	icons, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	usedIcons := map[string]struct{}{}
	iconPattern := regexp.MustCompile(`(?:lucide\.svg#|icon\(")([a-z-]+)`)
	for _, asset := range []string{"static/index.html", "static/app.js"} {
		source, err := assets.ReadFile(asset)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range iconPattern.FindAllStringSubmatch(string(source), -1) {
			usedIcons[match[1]] = struct{}{}
		}
	}
	for icon := range usedIcons {
		symbol := `<symbol id="` + icon + `" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">`
		if !strings.Contains(string(icons), symbol) {
			t.Errorf("lucide sprite lacks complete %q symbol", icon)
		}
	}

	request = httptest.NewRequest(http.MethodGet, "/", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "/ui/") {
		t.Error("index references an external UI asset")
	}
	for _, expected := range []string{
		`id="bundle-installer"`,
		`id="installer-dialog"`,
		`id="installer-acknowledgement"`,
		`id="installer-confirm"`,
		`id="installer-dialog-policy"`,
		`id="confirm-error"`,
		`id="confirm-progress"`,
		`href="/favicon.svg"`,
		`id="models-error"`,
		`mobile-dialog-close`,
		`class="app-toast"`,
		`aria-label="Generation progress"`,
		`role="dialog"`,
	} {
		if !strings.Contains(string(body), expected) {
			t.Errorf("index does not contain installer control %q", expected)
		}
	}
	app, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`/api/model-installer`,
		`/api/model-installer/install`,
		`/api/model-installer/cancel`,
		`policy_version: version`,
		`operation_id: operationID`,
		`document.createElement("progress")`,
		`inputRevisions`,
		`retryState`,
		`Promise.allSettled`,
		`init-engines`,
		`modelVersion`,
		`connectEventStream`,
		`selectMode`,
		`field-wide`,
		`engine-description`,
		`aria-describedby`,
		`compatibility-card`,
		`state.jobStatusKey`,
		`confirmError`,
	} {
		if !strings.Contains(string(app), expected) {
			t.Errorf("installer frontend does not contain %q", expected)
		}
	}
	if count := strings.Count(string(app), `new EventSource("/api/events")`); count != 1 {
		t.Errorf("frontend creates %d event streams, want exactly 1 construction site", count)
	}
	styles, err := assets.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(styles), `.ui-badge[data-state="running"]`) {
		t.Error("running badges must retain the neutral Core UI state treatment")
	}
}
