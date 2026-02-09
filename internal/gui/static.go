package gui

import (
	"io/fs"
	"net/http"
)

// StaticHandler returns an http.Handler for serving static files.
func StaticHandler() http.Handler {
	staticOnce.Do(func() {
		sub, err := fs.Sub(uiFS, "ui/static")
		if err != nil {
			staticHandler = http.NotFoundHandler()
			return
		}
		staticHandler = http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
	})
	return staticHandler
}
