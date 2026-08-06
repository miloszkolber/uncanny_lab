package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/*
var assets embed.FS

func Handler() (http.Handler, error) {
	root, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, err
	}
	return http.FileServer(http.FS(root)), nil
}
