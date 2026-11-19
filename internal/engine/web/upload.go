package web

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jancuk/semar/internal/domain"
)

// maxUploadBytes is the largest file that an upload reads.
const maxUploadBytes = 25 << 20

// uploadFile gives the file on the disk that an upload names. The path is below dir and stays below
// it after every link is followed, so a link in the folder cannot lead out of it. The error says what
// is wrong in plain words and never holds a path of the machine.
func uploadFile(dir, rel string) (string, error) {
	if msg := domain.UploadPathProblem(rel); msg != "" {
		return "", errors.New(msg)
	}
	if dir == "" {
		return "", errors.New("an upload needs the repository, and this run has none")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("the folder %s/ was not found", domain.UploadDir)
	}
	root, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("the folder %s/ was not found", domain.UploadDir)
	}
	real, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", fmt.Errorf("the file %s was not found below %s/", rel, domain.UploadDir)
	}
	if inside, err := filepath.Rel(root, real); err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("the path %s leaves %s/", rel, domain.UploadDir)
	}
	info, err := os.Stat(real)
	switch {
	case err != nil:
		return "", fmt.Errorf("the file %s could not be read", rel)
	case !info.Mode().IsRegular():
		return "", fmt.Errorf("%s is not a file", rel)
	case info.Size() > maxUploadBytes:
		return "", fmt.Errorf("the file %s is larger than %d MiB", rel, maxUploadBytes>>20)
	}
	return real, nil
}
