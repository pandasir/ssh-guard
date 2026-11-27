package main

import (
	"testing"

	"ssh-guard/internal/desktopui"

	"github.com/egoist/mygo/ui"
)

func TestConfigureDesktopRuntimeDefaultsAndOverrides(t *testing.T) {
	t.Run("windows defaults MYGO_GPU=0 and tight GC limits", func(t *testing.T) {
		vars := map[string]string{}
		var gotGC int
		var gotMem int64
		configureDesktopRuntime(runtimeEnv{
			goos:   "windows",
			getenv: func(k string) string { return vars[k] },
			setenv: func(k, v string) error { vars[k] = v; return nil },
			setGC: func(v int) int {
				gotGC = v
				return 100
			},
			setMemLimit: func(v int64) int64 {
				gotMem = v
				return -1
			},
		})
		if vars["MYGO_GPU"] != "0" {
			t.Fatalf("expected MYGO_GPU=0 on windows, got %q", vars["MYGO_GPU"])
		}
		if gotGC != defaultDesktopGOGC {
			t.Fatalf("expected GOGC=%d, got %d", defaultDesktopGOGC, gotGC)
		}
		if gotMem != defaultDesktopMemLimit {
			t.Fatalf("expected GOMEMLIMIT=%d, got %d", defaultDesktopMemLimit, gotMem)
		}
	})

	t.Run("windows respects explicit MYGO_GPU, GOGC, and GOMEMLIMIT overrides", func(t *testing.T) {
		vars := map[string]string{
			"MYGO_GPU":   "1",
			"GOGC":       "80",
			"GOMEMLIMIT": "128MiB",
		}
		gcCalled := false
		memCalled := false
		configureDesktopRuntime(runtimeEnv{
			goos:   "windows",
			getenv: func(k string) string { return vars[k] },
			setenv: func(k, v string) error { vars[k] = v; return nil },
			setGC: func(v int) int {
				gcCalled = true
				return 100
			},
			setMemLimit: func(v int64) int64 {
				memCalled = true
				return -1
			},
		})
		if vars["MYGO_GPU"] != "1" {
			t.Fatalf("expected explicit MYGO_GPU=1 preserved, got %q", vars["MYGO_GPU"])
		}
		if gcCalled {
			t.Fatal("expected explicit GOGC override to skip SetGCPercent")
		}
		if memCalled {
			t.Fatal("expected explicit GOMEMLIMIT override to skip SetMemoryLimit")
		}
	})

	t.Run("darwin and linux preserve default MYGO_GPU unset while applying GC defaults", func(t *testing.T) {
		for _, goos := range []string{"darwin", "linux"} {
			vars := map[string]string{}
			var gotGC int
			var gotMem int64
			configureDesktopRuntime(runtimeEnv{
				goos:   goos,
				getenv: func(k string) string { return vars[k] },
				setenv: func(k, v string) error { vars[k] = v; return nil },
				setGC: func(v int) int {
					gotGC = v
					return 100
				},
				setMemLimit: func(v int64) int64 {
					gotMem = v
					return -1
				},
			})
			if _, ok := vars["MYGO_GPU"]; ok {
				t.Fatalf("%s: expected MYGO_GPU to remain unset, got %q", goos, vars["MYGO_GPU"])
			}
			if gotGC != defaultDesktopGOGC || gotMem != defaultDesktopMemLimit {
				t.Fatalf("%s: expected GC defaults (%d, %d), got (%d, %d)", goos, defaultDesktopGOGC, defaultDesktopMemLimit, gotGC, gotMem)
			}
		}
	})
}

func TestDesktopAppBootstrapAndUnlockScreen(t *testing.T) {
	app, err := desktopui.NewApp(desktopui.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("desktopui.NewApp failed: %v", err)
	}
	defer app.Shutdown()

	tt := ui.NewTester(app.View, 1280, 820)
	if !tt.HasText("SSH-GUARD") {
		t.Fatalf("expected SSH-GUARD badge on unlock view, got %q", tt.Texts())
	}
}
