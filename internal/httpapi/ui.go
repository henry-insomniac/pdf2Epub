package httpapi

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed web/*
var webAssets embed.FS

func (a *API) registerUI() {
	assets, err := fs.Sub(webAssets, "web")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(assets))
	a.mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if requested == "." || requested == "" {
			requested = "index.html"
		}
		if requested == "index.html" {
			w.Header().Set("Cache-Control", "no-store")
			data, readErr := fs.ReadFile(assets, "index.html")
			if readErr != nil {
				http.Error(w, "UI unavailable", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(data)
			return
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		r.URL.Path = "/" + requested
		files.ServeHTTP(w, r)
	}))
}
