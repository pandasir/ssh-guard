package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	"ssh-guard/internal/api"
	"ssh-guard/internal/auth"
	"ssh-guard/internal/crypto"
	"ssh-guard/internal/store"
)

func main() {
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(50)
	}
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(96 << 20)
	}

	addr := flag.String("addr", "127.0.0.1:8080", "HTTP listen address (prefer 127.0.0.1 or WireGuard/Tailscale IP)")
	dataDir := flag.String("data", defaultWebDataDir(), "Directory for SQLite database and session recordings")
	tlsCert := flag.String("tls-cert", "", "Optional path to TLS certificate file (enables HTTPS/WSS)")
	tlsKey := flag.String("tls-key", "", "Optional path to TLS private key file")
	secureCookie := flag.Bool("secure-cookie", false, "Set Secure flag on session cookies (enable behind HTTPS proxy)")
	openBrowser := flag.Bool("open", false, "Automatically open panel in default browser")
	flag.Parse()

	dbPath := filepath.Join(*dataDir, "ssh_guard.db")
	recordingsDir := filepath.Join(*dataDir, "recordings")

	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer st.Close()

	vault := crypto.NewVault()
	defer vault.Lock() // Wipe KEK from memory on shutdown

	useTLS := *tlsCert != "" && *tlsKey != ""
	sessionMgr := auth.NewManager(useTLS || *secureCookie)
	handler := api.NewServer(st, vault, sessionMgr, recordingsDir)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	scheme := "http"
	if useTLS {
		scheme = "https"
	}
	url := scheme + "://" + *addr

	go func() {
		if useTLS {
			log.Printf("SSH-Guard Web Edition listening with TLS on %s (data: %s)", url, *dataDir)
			if err := srv.ListenAndServeTLS(*tlsCert, *tlsKey); err != nil && err != http.ErrServerClosed {
				log.Fatalf("server error: %v", err)
			}
		} else {
			log.Printf("SSH-Guard Web Edition listening on %s (data: %s)", url, *dataDir)
			log.Printf("Security tip: Keep bound to 127.0.0.1 or place behind Tailscale/Nginx HTTPS.")
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("server error: %v", err)
			}
		}
	}()

	if *openBrowser {
		go func() {
			time.Sleep(400 * time.Millisecond)
			launchBrowser(url)
		}()
	}

	<-stop
	log.Println("Shutting down SSH-Guard Web Edition and wiping in-memory KEK...")
	vault.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func defaultWebDataDir() string {
	if custom := os.Getenv("SSH_GUARD_DATA_DIR"); custom != "" {
		return custom
	}
	return "./data"
}

func launchBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
