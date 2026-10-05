package web

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

//go:embed static/index.html static/app.js static/styles.css static/favicon.svg static/theme.js static/no-js.css
var assets embed.FS

// uiRoot contains the complete checksum-addressed Mewa UI package.
// Local development can point UI_ROOT at the repository's ui directory.
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
			w.Header().Set("Cache-Control", "no-store")
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		app.ServeHTTP(w, r)
	}), nil
}

var versionedUIAsset = regexp.MustCompile(`^mewa-ui/[a-f0-9]{64}/[a-zA-Z0-9_./-]+$`)

func serveUIFromDisk(w http.ResponseWriter, r *http.Request) bool {
	name := strings.TrimPrefix(r.URL.Path, "/ui/")
	if name == "" || filepath.IsAbs(name) || strings.Contains(name, "..") || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
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
	root, err := filepath.EvalSymlinks(uiRoot())
	if err != nil {
		return false
	}
	realPath, err := filepath.EvalSymlinks(source)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(root, realPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return false
	}
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
	cache := "no-cache"
	if versionedUIAsset.MatchString(name) && filepath.ToSlash(filepath.Clean(name)) == name && r.URL.EscapedPath() == "/ui/"+name {
		cache = "public, max-age=31536000, immutable"
	}
	w.Header().Set("Cache-Control", cache)
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
	return true
}
