package web

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

const uiPrefix = "/ui/mewa-ui/510d5083135db0edd06415d36b2d9096b968e354aa86acba9c132f90ad8f4fdd/"

// The fixture exercises disk serving independently of authored HTML references.
func writeUIFixture(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		strings.TrimPrefix(uiPrefix, "/ui/") + "css/base.css":                 "/* mewa_ui — base */",
		strings.TrimPrefix(uiPrefix, "/ui/") + "css/tokens.css":               ".dark { color-scheme: dark; }",
		strings.TrimPrefix(uiPrefix, "/ui/") + "fonts/google-sans-code.woff2": "font fixture",
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEmbeddedAppFiles(t *testing.T) {
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	for path, expected := range map[string]string{
		"/styles.css":  "/* Uncanny Lab application styles",
		"/favicon.svg": `<svg xmlns="http://www.w3.org/2000/svg"`,
		"/theme.js":    `localStorage.getItem("mewa-ui-theme")`,
		"/no-js.css":   "#command-trigger",
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

func TestUIFromDisk(t *testing.T) {
	root := t.TempDir()
	writeUIFixture(t, root)
	t.Setenv("UI_ROOT", root)
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	for path, expected := range map[string]string{
		uiPrefix + "css/base.css":                 "mewa_ui",
		uiPrefix + "css/tokens.css":               ".dark",
		uiPrefix + "fonts/google-sans-code.woff2": "font fixture",
	} {
		code, body := getBody(t, handler, path)
		if code != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, code)
		}
		if !strings.Contains(body, expected) {
			t.Errorf("GET %s does not contain %q", path, expected)
		}
	}
	if code, _ := getBody(t, handler, "/ui/../web/embed.go"); code != http.StatusNotFound {
		t.Errorf("GET /ui/../web/embed.go status = %d, want 404", code)
	}
	if code, _ := getBody(t, handler, uiPrefix+"secret.txt"); code != http.StatusNotFound {
		t.Errorf("GET secret.txt status = %d, want 404", code)
	}
	request := httptest.NewRequest(http.MethodHead, uiPrefix+"fonts/google-sans-code.woff2", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.Len() != 0 || response.Header().Get("Content-Type") != "font/woff2" {
		t.Fatalf("HEAD font = %d, %s, body length %d", response.Code, response.Header().Get("Content-Type"), response.Body.Len())
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
	// Use the real imported package, not assets synthesized from the HTML under test.
	t.Setenv("UI_ROOT", filepath.Join("..", "ui"))
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	indexCode, index := getBody(t, handler, "/")
	if indexCode != http.StatusOK {
		t.Fatalf("GET / status = %d", indexCode)
	}
	// App-owned refs resolve from embed.FS; package refs resolve from the copied ui root.
	localRef := regexp.MustCompile(`(?:href|src)="(/[^"]+)"`)
	for _, match := range localRef.FindAllStringSubmatch(index, -1) {
		if code, _ := getBody(t, handler, match[1]); code != http.StatusOK {
			t.Errorf("GET %s referenced by index status = %d", match[1], code)
		}
	}
	// App icons are inlined; the core package intentionally keeps icons optional.
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
		`role="group"`,
		`data-dialog-close`,
		`data-alert-dialog-close`,
		`id="announcements" class="visually-hidden"`,
		`aria-label="Generation progress"`,
		`<dialog id="detail-dialog"`,
		`<dialog id="confirm-dialog"`,
		`<dialog id="installer-dialog"`,
		uiPrefix + `css/base.css`,
		uiPrefix + `css/tokens.css`,
		uiPrefix + `fonts/google-sans-code.css`,
		uiPrefix + `css/command-palette.css`,
		uiPrefix + `auto/alert-dialog.js`,
		uiPrefix + `auto/command-palette.js`,
		uiPrefix + `auto/dialog.js`,
		uiPrefix + `auto/tabs.js`,
		`<script src="/theme.js"></script>`,
		`href="/no-js.css"`,
		`class="app-nav"`,
		`class="page-description"`,
		`class="app-content"`,
		`class="alert-dialog"`,
		`command-palette-item`,
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
		`Runtime diagnostics`,
		`state.jobStatusKey`,
		`confirmError`,
		`dataset.alertDialogTrigger`,
		`dataset.dialogTrigger`,
		`tabs:activate`,
		`commandActions`,
		`className = "btn"`,
		`className = "badge"`,
		`relativeFormatter`,
		`dragOver`,
		`setDetailZoom`,
	} {
		if !strings.Contains(appCode, expected) {
			t.Errorf("frontend does not contain %q", expected)
		}
	}
	if count := strings.Count(appCode, `new EventSource("/api/events")`); count != 1 {
		t.Errorf("frontend creates %d event streams, want exactly 1 construction site", count)
	}
	for _, retired := range []string{"commandDialog._trigger", "destructive-outline", "data-size=", "/ui/src/", "/ui/components/"} {
		if strings.Contains(index+appCode, retired) {
			t.Errorf("frontend still uses retired contract %q", retired)
		}
	}
	if strings.Contains(index, "<script>") || strings.Contains(index, "<style>") {
		t.Error("embedded HTML contains a CSP-blocked inline script or stylesheet")
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
	tabOrderUpdate := strings.Index(appCode, "el.detailViewport.tabIndex = image.hidden ? -1 : 0;")
	if tabOrderUpdate < 0 {
		t.Error("detail image viewport tab order is not synchronized with image availability")
	}
	if !strings.Contains(appCode, `open.dataset.dialogTrigger = "detail-dialog"`) {
		t.Error("detail action must use the Dialog trigger contract")
	}
}

func TestCompletePackageRuntimeIsServedUnchanged(t *testing.T) {
	root := filepath.Join("..", "ui")
	t.Setenv("UI_ROOT", root)
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".js" && ext != ".css" && ext != ".woff2" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ui/"+filepath.ToSlash(rel), nil))
		want, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), want) {
			t.Errorf("%s is not served byte-exact (status %d)", rel, response.Code)
		}
		if response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Errorf("%s is not immutable", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range []string{"/", "/app.js", "/theme.js", "/styles.css", "/no-js.css"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, asset, nil))
		if response.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s must revalidate", asset)
		}
	}
}

func TestPackageDiskBoundaryRejectsLinksAndNonAssets(t *testing.T) {
	root := t.TempDir()
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "outside.css"), []byte("not a package asset"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(out, "outside.css"), filepath.Join(root, "linked.css")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UI_ROOT", root)
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []string{"/ui/escape/outside.css", "/ui/linked.css", "/ui/missing.css", "/ui/secret.txt", "/ui/../outside.css"} {
		if code, _ := getBody(t, handler, request); code != http.StatusNotFound {
			t.Errorf("%s status %d", request, code)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, request, nil))
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("failed path %s must not be cached", request)
		}
	}
}
