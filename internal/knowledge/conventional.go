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

// WithFolder puts the slash path of a folder in front of every path of list. The paths of
// knowledge start at the root of the repository, and Conventional starts at the folder that
// the person works in. An entry whose new path the configuration would refuse, for example
// because a folder name holds a back quote, is left out. An empty folder changes nothing.
func WithFolder(list []config.Knowledge, folder string) []config.Knowledge {
	if folder == "" {
		return list
	}
	var out []config.Knowledge
	for _, k := range list {
		k.Path = folder + "/" + k.Path
		if config.CheckKnowledgePath(k.Path) == nil {
			out = append(out, k)
		}
	}
	return out
}

// ConventionalBelow is Conventional for the folder of root that the slash path names, with
// the paths written from root. A link on the way to the folder ends the search.
func ConventionalBelow(root, folder string) []config.Knowledge {
	dir := root
	if folder != "" {
		for _, n := range strings.Split(folder, "/") {
			dir = filepath.Join(dir, n)
			if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
				return nil
			}
		}
	}
	return WithFolder(Conventional(dir), folder)
}
