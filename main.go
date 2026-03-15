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

	// Web server on port 8383 — chosen to avoid common port conflicts
	s := rweb.NewServer(rweb.ServerOptions{
		Address: ":8383",
		Verbose: true,
	})

	s.Use(rweb.RequestInfo) // request logging middleware

	app.RegisterRoutes(s)

	log.Println("CamCtl running at http://localhost:8383")
	log.Fatal(s.Run())
}
