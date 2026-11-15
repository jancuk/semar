package dashboard

import (
	"embed"
	"net/http"
	"path"
)

//go:embed ui/index.html ui/app.js ui/style.css
var ui embed.FS

var contentTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
}

func page(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := ui.ReadFile("ui/" + name)
		if err != nil {
			http.Error(w, "The page is missing from this build.", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", contentTypes[path.Ext(name)])
		_, _ = w.Write(data)
	}
}
