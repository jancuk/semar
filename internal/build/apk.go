package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"

	"github.com/jancuk/semar/internal/config"
)

const (
	// APKDigestLen is how many hexadecimal digits of the sha256 of an APK name its build.
	APKDigestLen = 40
	// maxAPK caps APK hashing at 4 GiB so a hostile or special file cannot exhaust memory or hang.
	maxAPK = 4 << 30
)

// APKPath is the absolute path of the APK that a service lists: the path itself when it is absolute,
// else a path below the repository.
func APKPath(repoRoot, apk string) string {
	if filepath.IsAbs(apk) {
		return filepath.Clean(apk)
	}
	return filepath.Join(repoRoot, filepath.FromSlash(apk))
}

// ResolveAPK returns the canonical path of an APK. An absolute path is cleaned as given; a relative
// path must stay inside repoRoot even after following symlinks.
func ResolveAPK(repoRoot, apk string) (string, error) {
	if filepath.IsAbs(apk) {
		return filepath.Clean(apk), nil
	}
	relPath := filepath.FromSlash(apk)
	if !filepath.IsLocal(relPath) || repoRoot == "" {
		return "", fmt.Errorf("the APK path %q is not inside the repository", apk)
	}
	root, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		return "", err
	}
	full, err := filepath.EvalSymlinks(filepath.Join(root, relPath))
	if err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(root, full); err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("the APK %s leaves the repository through a link", apk)
	}
	return full, nil
}

// APKDigest is the first 40 hexadecimal digits of the sha256 of a regular file at most 4 GiB.
func APKDigest(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("the file %s is not a regular file", path)
	}
	if info.Size() > maxAPK {
		return "", fmt.Errorf("the file %s is larger than 4 GiB", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, maxAPK+1))
	if err != nil {
		return "", err
	}
	if n > maxAPK {
		return "", fmt.Errorf("the file %s is larger than 4 GiB", path)
	}
	return hex.EncodeToString(h.Sum(nil))[:APKDigestLen], nil
}

// WithAPK adds, for each service that lists an APK and has no build probe and no declared commit, the
// build of that file: the digest of the APK is its commit. The app that a device runs is then the build
// that a result belongs to, and reading it needs no device. It returns the commits, and the services
// whose commit it added.
//
// The precedence of a build is the probe, then --build, then the APK, then the working copy that
// WithLocal reads. A file that is missing or cannot be read adds no build: the first step on a device
// then ends blocked with that reason.
func WithAPK(declared map[string]string, services []config.Service, repoRoot string) (commits map[string]string, apk []string) {
	commits = maps.Clone(declared)
	if commits == nil {
		commits = map[string]string{}
	}
	for _, s := range services {
		if _, named := commits[s.Name]; named || s.Build != nil || s.Android == nil || s.Android.APK == "" {
			continue
		}
		path, err := ResolveAPK(repoRoot, s.Android.APK)
		if err != nil {
			continue
		}
		digest, err := APKDigest(path)
		if err != nil {
			continue
		}
		commits[s.Name] = digest
		apk = append(apk, s.Name)
	}
	return commits, apk
}
