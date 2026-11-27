package main

import (
	"log"
	"os"
	"runtime"
	"runtime/debug"

	"ssh-guard/internal/desktopui"
)

const (
	defaultDesktopGOGC     = 25
	defaultDesktopMemLimit = 28 << 20 // 28 MiB steady-state budget
)

type runtimeEnv struct {
	goos        string
	getenv      func(string) string
	setenv      func(string, string) error
	setGC       func(int) int
	setMemLimit func(int64) int64
}

func configureDesktopRuntime(env runtimeEnv) {
	// On Windows, default to MyGo's multi-core CPU rasterizer + GDI blit to avoid
	// 25-35 MiB of persistent D3D11/DXGI driver overhead while preserving explicit
	// MYGO_GPU=1 opt-in and leaving macOS Metal / Linux defaults untouched.
	if env.goos == "windows" && env.getenv("MYGO_GPU") == "" {
		_ = env.setenv("MYGO_GPU", "0")
	}

	// Keep steady-state Go heap tight on desktop; Argon2id KEK derivation raises
	// GOMEMLIMIT transiently to 96 MiB and immediately reclaims OS memory on exit.
	if env.getenv("GOGC") == "" {
		env.setGC(defaultDesktopGOGC)
	}
	if env.getenv("GOMEMLIMIT") == "" {
		env.setMemLimit(defaultDesktopMemLimit)
	}
}

func main() {
	configureDesktopRuntime(runtimeEnv{
		goos:        runtime.GOOS,
		getenv:      os.Getenv,
		setenv:      os.Setenv,
		setGC:       debug.SetGCPercent,
		setMemLimit: debug.SetMemoryLimit,
	})

	app, err := desktopui.NewApp(desktopui.Options{})
	if err != nil {
		log.Fatalf("failed to initialize SSH-Guard Desktop: %v", err)
	}
	if err := app.Run(); err != nil {
		log.Fatalf("mygo runtime error: %v", err)
	}
}
