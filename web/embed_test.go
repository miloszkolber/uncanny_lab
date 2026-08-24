package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func getBody(t *testing.T, handler http.Handler, path string) (int, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.Code, string(body)
}

func TestEmbeddedUIFoundations(t *testing.T) {
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	for path, expected := range map[string]string{
		"/styles.css":        "/* Uncanny Lab application styles",
		"/ui/src/base.css":   "mewa_ui",
		"/ui/src/tokens.css": ".dark",
		"/favicon.svg":       `<svg xmlns="http://www.w3.org/2000/svg"`,
	} {
		code, body := getBody(t, handler, path)
		if code != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, code)
		}
		if !strings.Contains(body, expected) {
			t.Errorf("GET %s does not contain %q", path, expected)
		}
	}
	_, styles := getBody(t, handler, "/styles.css")
	if strings.Contains(styles, "--ui-") || strings.Contains(styles, "Core UI") {
		t.Error("application styles still reference the retired Core UI foundation")
	}
}

func TestEmbeddedUISkipLinkTarget(t *testing.T) {
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	code, index := getBody(t, handler, "/")
	if code != http.StatusOK {
		t.Fatalf("GET / status = %d", code)
	}
	if !strings.Contains(index, `<a class="skip-link" href="#main-content">Skip to main content</a>`) {
		t.Fatal("embedded index is missing the static skip link")
	}
	if !strings.Contains(index, `<main id="main-content" tabindex="-1">`) {
		t.Fatal("embedded index is missing the stable focusable main target")
	}
	_, appCode := getBody(t, handler, "/app.js")
	if !strings.Contains(appCode, `document.querySelector(".skip-link")?.addEventListener("click", focusMainFromSkip)`) || !strings.Contains(appCode, `event.preventDefault(); document.querySelector("#main-content")?.focus();`) {
		t.Fatal("skip-link handling must focus main without changing the route")
	}
}

func TestEmbeddedUIAssetsAreSelfContained(t *testing.T) {
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	indexCode, index := getBody(t, handler, "/")
	if indexCode != http.StatusOK {
		t.Fatalf("GET / status = %d", indexCode)
	}
	// Every local stylesheet, script, image, and font referenced by the UI must
	// resolve inside the embedded filesystem.
	localRef := regexp.MustCompile(`(?:href|src)="(/[^"]+)"`)
	for _, match := range localRef.FindAllStringSubmatch(index, -1) {
		if code, _ := getBody(t, handler, match[1]); code != http.StatusOK {
			t.Errorf("GET %s referenced by index status = %d", match[1], code)
		}
	}
	// Icons are inlined or loaded from the vendored standalone Lucide files.
	appCode := func() string {
		request := httptest.NewRequest(http.MethodGet, "/app.js", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}()
	iconPattern := regexp.MustCompile(`icon\("([a-z-]+)"`)
	for _, source := range []string{index, appCode} {
		for _, match := range iconPattern.FindAllStringSubmatch(source, -1) {
			path := "/ui/src/icons/" + match[1] + ".svg"
			code, icon := getBody(t, handler, path)
			if code != http.StatusOK {
				t.Errorf("GET %s status = %d", path, code)
				continue
			}
			for _, expected := range []string{"viewBox=\"0 0 24 24\"", "stroke=\"currentColor\""} {
				if !strings.Contains(icon, expected) {
					t.Errorf("%s lacks %q", path, expected)
				}
			}
		}
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
		`id="history-status-filter"`,
		`id="history-summary"`,
		`id="command-dialog"`,
		`id="command-search"`,
		`id="detail-zoom-in"`,
		`id="detail-zoom-out"`,
		`role="toolbar"`,
		`data-close-dialog`,
		`class="app-toast"`,
		`aria-label="Generation progress"`,
		`<dialog id="detail-dialog"`,
		`<dialog id="confirm-dialog"`,
		`<dialog id="installer-dialog"`,
		`/ui/src/base.css`,
		`/ui/src/tokens.css`,
		`/ui/components/command-palette/command-palette.css`,
		`role="dialog"`,
	} {
		if !strings.Contains(index, expected) {
			t.Errorf("index does not contain %q", expected)
		}
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
		`showModal()`,
		`.close()`,
		`className = "btn"`,
		`className = "badge"`,
		`relativeFormatter`,
		`dragOver`,
		`command-palette-item`,
		`setDetailZoom`,
	} {
		if !strings.Contains(appCode, expected) {
			t.Errorf("frontend does not contain %q", expected)
		}
	}
	if count := strings.Count(appCode, `new EventSource("/api/events")`); count != 1 {
		t.Errorf("frontend creates %d event streams, want exactly 1 construction site", count)
	}
}

func TestDetailImageViewportTabOrder(t *testing.T) {
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	_, index := getBody(t, handler, "/")
	_, appCode := getBody(t, handler, "/app.js")

	viewport := regexp.MustCompile(`<div id="detail-image-viewport"[^>]*>`).FindString(index)
	if viewport == "" {
		t.Fatal("detail image viewport is missing")
	}
	if strings.Contains(viewport, "tabindex=") {
		t.Error("detail image viewport must not have a static tabindex")
	}
	tabOrderUpdate := strings.Index(appCode, "el.detailViewport.tabIndex = el.detailImage.hidden ? -1 : 0;")
	dialogOpen := strings.Index(appCode, "dialog.showModal();")
	if tabOrderUpdate < 0 {
		t.Error("detail image viewport tab order is not synchronized with image availability")
	}
	if dialogOpen < 0 || tabOrderUpdate > dialogOpen {
		t.Error("detail image viewport tab order must be synchronized before the dialog opens")
	}
}
