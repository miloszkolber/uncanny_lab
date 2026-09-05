package web

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

//go:embed static/index.html static/app.js static/styles.css static/favicon.svg
var assets embed.FS

// uiRoot is the baked mewa_ui checkout (see Dockerfile COPY --from=mewa_ui,
// same pattern as cuddler). Local development can point UI_ROOT at
// /repo/mewa_ui/library or web/static/ui.
func uiRoot() string {
	if value := os.Getenv("UI_ROOT"); value != "" {
		return value
	}
	return "/ui"
}

func Handler() (http.Handler, error) {
	root, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, err
	}
	app := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/ui/") {
			if serveUIFromDisk(w, r) {
				return
			}
		}
		app.ServeHTTP(w, r)
	}), nil
}

func serveUIFromDisk(w http.ResponseWriter, r *http.Request) bool {
	name := strings.TrimPrefix(r.URL.Path, "/ui/")
	if name == "" || strings.Contains(name, "..") {
		return false
	}
	var contentType string
	switch {
	case strings.HasSuffix(name, ".css"):
		contentType = "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		contentType = "application/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".svg"):
		contentType = "image/svg+xml"
	case strings.HasSuffix(name, ".woff2"):
		contentType = "font/woff2"
	default:
		return false
	}
	source := filepath.Join(uiRoot(), name)
	info, err := os.Lstat(source)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return false
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
	return true
}
