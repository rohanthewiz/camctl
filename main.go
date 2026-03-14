package main

import (
	"camctl/handlers"
	"camctl/storage"
	"log"

	"github.com/rohanthewiz/rweb"
)

func main() {
	// Initialize DuckDB storage — creates camctl.db on first run,
	// migrates any existing cameras.json / presets.json automatically.
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
