// migrate-legacy imports the previous Worker/D1 SQLite state into fulibu.db.
// Run it once before switching the production service to the Go API.
package main

import (
	"flag"
	"log"

	"fulibu-go/internal/database"
)

func main() {
	legacy := flag.String("legacy", "", "path to the previous D1 SQLite database")
	dataDir := flag.String("data-dir", "./data", "directory for fulibu.db")
	flag.Parse()
	if *legacy == "" {
		log.Fatal("-legacy is required")
	}
	db, err := database.Open(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := database.ImportLegacy(db, *legacy); err != nil {
		log.Fatal(err)
	}
	log.Println("legacy data imported successfully")
}
