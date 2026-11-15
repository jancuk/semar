package provider

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// FindBinary returns the absolute path of a harness binary: the path in the variable
// envName when it is set, else name on PATH. The path is never read from the
// configuration, which the repository under test owns.
func FindBinary(lookup func(string) (string, bool), envName, name string) (string, *Error) {
	notInstalled := func(format string, args ...any) (string, *Error) {
		return "", &Error{Class: NotInstalled, Detail: fmt.Sprintf(format, args...)}
	}
	if p, ok := lookup(envName); ok && p != "" {
		if !filepath.IsAbs(p) {
			return notInstalled("%s is %q, which is not an absolute path", envName, p)
		}
		info, err := os.Stat(p)
		switch {
		case err != nil:
			return notInstalled("%s names %q, which cannot be read: %v", envName, p, errors.Unwrap(err))
		case !info.Mode().IsRegular():
			return notInstalled("%s names %q, which is not a regular file", envName, p)
		case runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0:
			return notInstalled("%s names %q, which is not executable", envName, p)
		}
		return p, nil
	}
	path, err := exec.LookPath(name)
	switch {
	case errors.Is(err, exec.ErrDot):
		return notInstalled("the binary %q was found on PATH only through a relative directory; set %s to an absolute path", name, envName)
	case err != nil:
		return notInstalled("the binary %q was not found on PATH, and %s is not set", name, envName)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return notInstalled("the binary %q on PATH has no absolute path: %v", path, err)
	}
	return abs, nil
}

// RefuseInside refuses a binary that the repository under test, or the copy of it, could
// have supplied: a PATH entry such as <repository>/bin would otherwise start the repository's
// own program.
func RefuseInside(bin, repoRoot, workdir string) *Error {
	realBin, err := filepath.EvalSymlinks(bin)
	if err != nil {
		return &Error{Class: NotInstalled, Detail: fmt.Sprintf("the binary %q cannot be resolved: %v", bin, errors.Unwrap(err))}
	}
	for _, d := range []struct{ dir, what string }{
		{repoRoot, "the repository under test"},
		{workdir, "the working directory of the job"},
	} {
		if d.dir == "" {
			continue
		}
		realDir, err := filepath.EvalSymlinks(d.dir)
		if err != nil {
			return &Error{Class: ValidationError, Detail: fmt.Sprintf("%s, %q, cannot be resolved", d.what, d.dir)}
		}
		if rel, err := filepath.Rel(realDir, realBin); err == nil && filepath.IsLocal(rel) {
			return &Error{Class: PermissionError, Detail: fmt.Sprintf("the binary %q lies inside %s, and a harness inside the repository under test is not started", bin, d.what)}
		}
	}
	return nil
}
