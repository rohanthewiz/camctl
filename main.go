package main

import (
	"camctl/handlers"
	"camctl/storage"
	"log"
	"os"
	"path/filepath"

	"github.com/rohanthewiz/rweb"
)

func main() {
	// Resolve ~/.camctl/ as the app's config/data directory.
	// Created on first run; on subsequent runs we just change into it.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("failed to determine home directory: %v", err)
	}
	dataDir := filepath.Join(homeDir, ".camctl")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("failed to create data directory %s: %v", dataDir, err)
	}
	if err := os.Chdir(dataDir); err != nil {
		log.Fatalf("failed to change to data directory %s: %v", dataDir, err)
	}

	// Initialize DuckDB storage — creates camctl.db on first run,
	// migrates any existing cameras.json / presets.json automatically.
	// DB is created in the current directory (~/.camctl/).
	store, err := storage.Open("camctl.db")
	if err != nil {
		log.Fatalf("failed to initialize storage: %v", err)
	}
	defer store.Close()

	app := handlers.NewApp(store)

	// Web server port — 8383 by default (chosen to avoid common port conflicts),
	// overridable via CAMCTL_PORT so the macOS app wrapper (scripts/mac-install.sh)
	// or a second instance can pick a port without a rebuild.
	port := os.Getenv("CAMCTL_PORT")
	if port == "" {
		port = "8383"
	}
	s := rweb.NewServer(rweb.ServerOptions{
		Address: ":" + port,
		Verbose: true,
	})

	s.Use(rweb.RequestInfo) // request logging middleware

	app.RegisterRoutes(s)

	log.Printf("CamCtl running at http://localhost:%s", port)
	log.Fatal(s.Run())
}
