package web_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jancuk/semar/internal/engine/web"
)

func TestLoginWithoutChrome(t *testing.T) {
	_, err := web.Login(context.Background(), web.LoginOptions{Chrome: "/does/not/exist", Home: t.TempDir(), Environment: "test", URL: "http://x.test"})
	if err == nil {
		t.Fatal("Login started with no Chrome")
	}
}

func TestProfileDirIsBelowTheHomeAndNeverTheUsersOwn(t *testing.T) {
	dir, err := web.ProfileDir("/h", "uat", "ana")
	if err != nil || dir != filepath.Join("/h", "browser", "uat", "ana") {
		t.Errorf("dir = %q, %v", dir, err)
	}
	if dir, _ := web.ProfileDir("/h", "uat", ""); dir != filepath.Join("/h", "browser", "uat", "default") {
		t.Errorf("default account: %q", dir)
	}
	for _, bad := range [][2]string{{"..", "ana"}, {"uat", "../x"}, {"a/b", "ana"}, {"", "ana"}} {
		if _, err := web.ProfileDir("/h", bad[0], bad[1]); err == nil {
			t.Errorf("ProfileDir accepted %v", bad)
		}
	}
	if _, err := web.ProfileDir("", "uat", "ana"); err == nil {
		t.Error("ProfileDir accepted no home")
	}
}
