// 服务启动入口：加载配置、打开数据库、启动定时任务并提供 API 与前端静态页面。

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
	dataLock, err := database.AcquireDataLock(dataDir)
	if err != nil {
		log.Fatal(err)
	}
	defer dataLock.Close()
	db, err := database.Open(dataDir)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	backup, err := database.StartMySQLBackup(ctx, db, os.Getenv("MYSQL_DSN"))
	if err != nil {
		log.Fatalf("initialize SQLite backup capture: %v", err)
	}
	defer backup.Close()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	api := httpapi.New(db)
	handler := serveApp(api, os.Getenv("STATIC_DIR"))
	log.Printf("Fulibu listening on :%s", port)
	httpapi.StartDailyJobs(ctx, db)
	server := &http.Server{Addr: ":" + port, Handler: handler}
	go /* 收到退出信号后，在十秒超时内优雅关闭 HTTP 服务。 */ func() {
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
	return http.HandlerFunc( /* 将 API 请求转交后端，其余请求按静态文件或单页应用入口处理。 */ func(w http.ResponseWriter, r *http.Request) {
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
