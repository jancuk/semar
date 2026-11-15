// Package knowledge finds and reads the documents that a team keeps next to its code.
package knowledge

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/jancuk/semar/internal/config"
)

// conventionalAPI lists the paths of an API document in the order of preference.
var conventionalAPI = []string{
	"docs/openapi.yaml", "docs/openapi.yml", "docs/openapi.json",
	"docs/swagger.yaml", "docs/swagger.yml", "docs/swagger.json",
	"api/openapi.yaml", "api/openapi.yml", "api/openapi.json",
	"api/swagger.yaml", "api/swagger.yml", "api/swagger.json",
	"openapi.yaml", "openapi.yml", "openapi.json",
	"swagger.yaml", "swagger.yml", "swagger.json",
}

// Conventional returns the sources that the naming convention finds under dir: the first
// API document of the list, with the ID api, and the docs folder, with the ID docs. It
// uses Lstat only, so a link never counts and is never followed.
func Conventional(dir string) []config.Knowledge {
	var found []config.Knowledge
	for _, p := range conventionalAPI {
		if isRegular(dir, p) {
			found = append(found, config.Knowledge{ID: "api", Kind: config.KnowledgeOpenAPI, Path: p})
			break
		}
	}
	if fi, err := os.Lstat(filepath.Join(dir, "docs")); err == nil && fi.IsDir() {
		found = append(found, config.Knowledge{ID: "docs", Kind: config.KnowledgeFiles, Path: "docs"})
	}
	return found
}

// isRegular reports whether rel is a regular file under dir. Every folder on the way is
// checked too, because Lstat follows a link that is not the last name.
func isRegular(dir, rel string) bool {
	names := strings.Split(rel, "/")
	cur := dir
	for i, n := range names {
		cur = filepath.Join(cur, n)
		fi, err := os.Lstat(cur)
		if err != nil {
			return false
		}
		if i < len(names)-1 {
			if !fi.IsDir() {
				return false
			}
			continue
		}
		return fi.Mode().IsRegular()
	}
	return false
}
