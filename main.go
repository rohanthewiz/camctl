package main

import (
	"camctl/handlers"
	"camctl/presets"
	"log"

	"github.com/rohanthewiz/rweb"
)

func main() {
	// Initialize preset storage — creates presets.json with defaults on first run
	presetStore, err := presets.NewStore("")
	if err != nil {
		log.Fatalf("failed to initialize presets: %v", err)
	}

	app := handlers.NewApp(presetStore)

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
