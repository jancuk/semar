package knowledge

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/openapi"
)

// MaxFiles bounds the count of the files of a folder. A source with more files than this
// has Files set to MaxFiles+1.
const MaxFiles = 2000

const maxAPIBytes = 5 << 20

// Source is a configured source as found in the copy of the source.
type Source struct {
	ID, Kind, Path, Service string
	Problem                 string            // why the planner cannot use it; "" when it can
	Folder                  bool              // Path is a folder
	Files                   int               // the regular files under Path; MaxFiles+1 means more than MaxFiles
	API                     *openapi.Document // an openapi source that was read
	Note                    string            // why an openapi source could not be read as an API document
}

// Usable reports whether the planner can read the source.
func (s Source) Usable() bool { return s.Problem == "" }

// Holds reports whether file, a slash path of the source copy, is Path or is under Path.
func (s Source) Holds(file string) bool {
	file = path.Clean(file)
	return file == s.Path || strings.HasPrefix(file, s.Path+"/")
}

// Resolve finds every configured source in sourceDir, the copy of the source at the head
// commit. A source that cannot be used gets a Problem and does not stop the others. Every
// file is opened through a root at sourceDir, so no path leaves the copy.
func Resolve(sourceDir string, list []config.Knowledge) ([]Source, error) {
	if len(list) == 0 {
		return nil, nil
	}
	root, err := os.OpenRoot(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("open the copy of the source to find the documents: %w", err)
	}
	defer root.Close()
	out := make([]Source, len(list))
	for i, k := range list {
		out[i] = resolve(root, k)
	}
	return out, nil
}

func resolve(root *os.Root, k config.Knowledge) Source {
	s := Source{ID: k.ID, Kind: k.Kind, Path: k.Path, Service: k.Service}
	native := filepath.FromSlash(k.Path)
	info, err := root.Lstat(native)
	switch {
	case err != nil:
		s.Problem = k.Path + " is not in the copy of the source"
		return s
	case info.IsDir() && k.Kind == config.KnowledgeOpenAPI:
		s.Problem = k.Path + " is a folder, and an API document is one file"
		return s
	case info.IsDir():
		s.Folder = true
		s.Files, err = countFiles(root, k.Path)
		if err != nil {
			s.Problem = k.Path + " cannot be read in the copy of the source"
		}
		return s
	case !info.Mode().IsRegular():
		s.Problem = k.Path + " is neither a file nor a folder in the copy of the source"
		return s
	}
	s.Files = 1
	if k.Kind != config.KnowledgeOpenAPI {
		return s
	}
	data, err := readAtMost(root, native, maxAPIBytes+1)
	if err != nil {
		s.Problem = k.Path + " cannot be read in the copy of the source"
		return s
	}
	doc, err := openapi.Parse(data, k.Path)
	if err != nil {
		s.Note = strings.TrimPrefix(err.Error(), k.Path+": ")
		return s
	}
	s.API = &doc
	return s
}

var errEnough = errors.New("enough files")

// countFiles counts the regular files under dir and stops after MaxFiles+1. A link is
// never followed.
func countFiles(root *os.Root, dir string) (int, error) {
	n := 0
	err := fs.WalkDir(root.FS(), dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			n++
			if n > MaxFiles {
				return errEnough
			}
		}
		return nil
	})
	if errors.Is(err, errEnough) {
		err = nil
	}
	return n, err
}

func readAtMost(root *os.Root, name string, limit int64) ([]byte, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}
