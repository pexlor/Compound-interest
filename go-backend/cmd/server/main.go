// Command server is the composition root. Business HTTP code and persistence
// implementation deliberately live outside this package.
package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"fulibu-go/internal/database"
	"fulibu-go/internal/httpapi"
)

func main() {
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "./data"
	}
	db, err := database.Open(dataDir)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	api := httpapi.New(db)
	handler := serveApp(api, os.Getenv("STATIC_DIR"))
	log.Printf("Fulibu listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, handler))
}

// serveApp makes the API and the Vite single-page application available from
// one origin. This avoids a second proxy process and keeps browser cookies
// same-origin in the native deployment.
func serveApp(api http.Handler, staticDir string) http.Handler {
	if staticDir == "" {
		return api
	}
	index := filepath.Join(staticDir, "index.html")
	files := http.FileServer(http.Dir(staticDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" {
			api.ServeHTTP(w, r)
			return
		}
		// Existing files are served normally; all other paths are client-side
		// routes and therefore receive the SPA entry point.
		if r.URL.Path != "/" {
			if _, err := os.Stat(filepath.Join(staticDir, filepath.Clean(r.URL.Path))); err != nil {
				http.ServeFile(w, r, index)
				return
			}
		}
		files.ServeHTTP(w, r)
	})
}
