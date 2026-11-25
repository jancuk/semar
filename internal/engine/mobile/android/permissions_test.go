package android_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/engine/mobile/mobiletest"
)

func TestParseRuntimePermissions(t *testing.T) {
	for name, c := range map[string]struct {
		dump string
		want []string
	}{
		"a real dump: runtime block first, then requested ones that are not install, background location last": {
			testdata(t, "dumpsys_package_permissions.txt"),
			[]string{
				"android.permission.POST_NOTIFICATIONS", "android.permission.ACCESS_FINE_LOCATION", "android.permission.ACCESS_COARSE_LOCATION",
				"android.permission.CAMERA", "android.permission.SCHEDULE_EXACT_ALARM", "android.permission.ACCESS_BACKGROUND_LOCATION",
			},
		},
		"a runtime block alone": {
			"    User 0: installed=true\n      runtime permissions:\n        android.permission.CAMERA: granted=false, flags=[]\n      disabledComponents:\n        a.b.C\n",
			[]string{"android.permission.CAMERA"},
		},
		"requested permissions when no runtime block lists them": {
			"    requested permissions:\n      android.permission.CAMERA\n      android.permission.INTERNET\n    install permissions:\n      android.permission.INTERNET: granted=true\n",
			[]string{"android.permission.CAMERA"},
		},
		"a name in two blocks once": {
			"    requested permissions:\n      android.permission.CAMERA\n    runtime permissions:\n      android.permission.CAMERA: granted=true\n",
			[]string{"android.permission.CAMERA"},
		},
		"a prose line is not a permission": {
			"      runtime permissions:\n        no permissions here: granted=true\n",
			nil,
		},
		"no permission block": {"Packages:\n  Package [a.b] (1):\n", nil},
		"empty":               {"", nil},
	} {
		if got := android.ParseRuntimePermissions(c.dump); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %v\nwant %v", name, got, c.want)
		}
	}
}

func grantsOf(r *rig) []string {
	var out []string
	for _, c := range r.adb.Shell() {
		if strings.Contains(c, "'pm' 'grant'") {
			out = append(out, strings.TrimPrefix(c, "'pm' 'grant' 'com.example.shop' "))
		}
	}
	return out
}

func TestPermissionsAllGrantsWhatTheAppRequests(t *testing.T) {
	r := newRig(t, func(_ *rig, c *mobiletest.ADBConfig) {
		c.Canned = []mobiletest.Canned{
			{Match: "'dumpsys' 'package' 'com.example.shop'", Out: testdata(t, "dumpsys_package_permissions.txt")},
			{
				Match: "'android.permission.SCHEDULE_EXACT_ALARM'", RC: 255,
				Err: "\nException occurred while executing 'grant':\njava.lang.SecurityException: Permission android.permission.SCHEDULE_EXACT_ALARM requested by package com.example.shop is not a changeable permission type\n\tat com.android.server.Foo\n",
			},
		}
	})
	r.opts.Apps = map[string]android.App{"shop": {Package: pkgShop, Reset: config.ResetClear, Permissions: config.Permissions{All: true}}}
	d := r.driver(r.platform())
	if err := d.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"'android.permission.POST_NOTIFICATIONS'", "'android.permission.ACCESS_FINE_LOCATION'", "'android.permission.ACCESS_COARSE_LOCATION'",
		"'android.permission.CAMERA'", "'android.permission.SCHEDULE_EXACT_ALARM'", "'android.permission.ACCESS_BACKGROUND_LOCATION'",
	}
	if got := grantsOf(r); !reflect.DeepEqual(got, want) {
		t.Errorf("grants:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestPermissionsAllStillFailsOnOtherGrantErrors(t *testing.T) {
	r := newRig(t, func(_ *rig, c *mobiletest.ADBConfig) {
		c.Canned = []mobiletest.Canned{
			{Match: "'dumpsys' 'package' 'com.example.shop'", Out: "      runtime permissions:\n        android.permission.CAMERA: granted=false\n"},
			{Match: "'pm' 'grant'", RC: 255, Err: "Exception occurred while executing 'grant':\njava.lang.SecurityException: Package com.example.shop has no permission to grant"},
		}
	})
	r.opts.Apps = map[string]android.App{"shop": {Package: pkgShop, Permissions: config.Permissions{All: true}}}
	err := r.driver(r.platform()).Reset(context.Background())
	if err == nil || !strings.Contains(err.Error(), "the permission android.permission.CAMERA could not be granted") {
		t.Errorf("error = %v", err)
	}
}

func TestAPermissionListDoesNotReadThePackage(t *testing.T) {
	r := newRig(t)
	r.opts.Apps = map[string]android.App{"shop": {Package: pkgShop, Permissions: config.Permissions{Names: []string{"android.permission.CAMERA"}}}}
	if err := r.driver(r.platform()).Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(r.adb.Shell(), "\n"), "'dumpsys' 'package'") {
		t.Error("dumpsys package was run for a list of names")
	}
}
