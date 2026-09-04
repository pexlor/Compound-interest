// Command server is the composition root. Business HTTP code and persistence
// implementation deliberately live outside this package.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"fulibu-go/internal/database"
	"fulibu-go/internal/httpapi"
)

// main 启动应用并加载运行所需的配置。
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	httpapi.StartDailyJobs(ctx, db)
	server := &http.Server{Addr: ":" + port, Handler: handler}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// serveApp 将 API 与前端单页应用托管在同一来源下。
// 它将 API 与 Vite 单页应用发布到同一个来源，避免浏览器 Cookie 跨域。
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
