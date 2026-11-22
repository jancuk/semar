package build_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/build"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
)

func apkService(name, apk string) config.Service {
	return config.Service{Name: name, BaseURL: "https://" + name + ".example.com", Android: &config.ServiceAndroid{AppPackage: "com.example.shop", APK: apk}}
}

func writeAPK(t *testing.T, dir, rel, content string) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWithAPKTakesTheFirst40DigitsOfTheSha256(t *testing.T) {
	repo := t.TempDir()
	writeAPK(t, repo, "app/debug.apk", "an apk")
	sum := sha256.Sum256([]byte("an apk"))
	want := hex.EncodeToString(sum[:])[:40]

	commits, apk := build.WithAPK(nil, []config.Service{apkService("shop", "app/debug.apk")}, repo)
	if commits["shop"] != want || len(apk) != 1 || apk[0] != "shop" {
		t.Fatalf("commits = %v, apk = %v, want shop at %s", commits, apk, want)
	}
	got, err := build.NewDeclared(commits, nil).APK(apk...).Probe(context.Background(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if got != (domain.BuildRef{Service: "shop", Commit: want, Declared: true, APK: true}) {
		t.Errorf("build = %+v, want a declared APK build with no Local", got)
	}
}

func TestWithAPKTakesAnAbsolutePathAndSeesAChange(t *testing.T) {
	path := writeAPK(t, t.TempDir(), "x.apk", "one")
	svc := []config.Service{apkService("shop", path)}
	first, _ := build.WithAPK(nil, svc, t.TempDir())
	writeAPK(t, filepath.Dir(path), "x.apk", "two")
	second, _ := build.WithAPK(nil, svc, t.TempDir())
	if first["shop"] == "" || first["shop"] == second["shop"] {
		t.Errorf("builds = %q then %q, want two different digests", first["shop"], second["shop"])
	}
}

func TestWithAPKLeavesAServiceThatHasABuildAlone(t *testing.T) {
	repo := t.TempDir()
	writeAPK(t, repo, "a.apk", "x")
	probed := apkService("probed", "a.apk")
	probed.Build = &config.BuildProbe{}
	commits, apk := build.WithAPK(map[string]string{"named": "abcdef1"}, []config.Service{probed, apkService("named", "a.apk"), apkService("shop", "a.apk")}, repo)
	if _, ok := commits["probed"]; ok {
		t.Error("a service with a build probe got an APK build")
	}
	if commits["named"] != "abcdef1" {
		t.Errorf("named = %q, want --build to win over the APK", commits["named"])
	}
	if len(apk) != 1 || apk[0] != "shop" {
		t.Errorf("apk = %v, want only shop", apk)
	}
}

func TestWithAPKAddsNoBuildForAMissingFileOrNoAPK(t *testing.T) {
	plain := config.Service{Name: "api", BaseURL: "https://api.example.com"}
	commits, apk := build.WithAPK(nil, []config.Service{apkService("shop", "missing.apk"), plain}, t.TempDir())
	if len(commits) != 0 || len(apk) != 0 {
		t.Errorf("commits = %v, apk = %v, want none", commits, apk)
	}
}

func TestAPKBeatsTheWorkingCopy(t *testing.T) {
	repo := t.TempDir()
	writeAPK(t, repo, "a.apk", "x")
	svc := apkService("shop", "a.apk")
	svc.BaseURL = "http://127.0.0.1:3000"
	commits, apk := build.WithAPK(nil, []config.Service{svc}, repo)
	commits, local, err := build.WithLocal(context.Background(), commits, []config.Service{svc}, fakeRepo{head: sha})
	if err != nil || len(local) != 0 || commits["shop"] == sha {
		t.Fatalf("commits = %v, local = %v, err = %v, want the APK digest and no working copy", commits, local, err)
	}
	_ = apk
}

func TestAPKDigestRefusesNonRegularFilesAndEscapingSymlinks(t *testing.T) {
	dir := t.TempDir()
	if _, err := build.APKDigest(dir); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("directory digest err = %v, want non-regular refusal", err)
	}
	if runtime.GOOS != "windows" {
		if _, err := build.APKDigest("/dev/zero"); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("/dev/zero digest err = %v, want non-regular refusal", err)
		}
		outside := writeAPK(t, t.TempDir(), "outside.apk", "secret")
		repo := t.TempDir()
		link := filepath.Join(repo, "escape.apk")
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
		if _, err := build.APKDigest(link); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("symlink digest err = %v, want non-regular refusal", err)
		}
		commits, apk := build.WithAPK(nil, []config.Service{apkService("shop", "escape.apk")}, repo)
		if len(commits) != 0 || len(apk) != 0 {
			t.Errorf("escaping symlink got build: commits=%v apk=%v", commits, apk)
		}
	}
}
