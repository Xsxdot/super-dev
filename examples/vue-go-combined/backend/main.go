// Package main provides the Vue + Go combined pipeline example.
//
// Responsibilities:
//   - Expose /health for deployment health checks.
//   - Expose /api/info with backend metadata.
//   - Serve the built Vue dist directory with SPA fallback.
//
// Boundaries:
//   - Does not require Nginx or DNS.
//   - Does not access databases or queues.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "18086"
	}
	distDir := os.Getenv("DIST_DIR")
	if distDir == "" {
		distDir = "dist"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"app":      "vue-go-combined",
			"language": "go+vue",
			"version":  os.Getenv("APP_VERSION"),
		})
	})
	mux.HandleFunc("/", spaHandler(distDir))
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

func spaHandler(distDir string) http.HandlerFunc {
	fileServer := http.FileServer(http.Dir(distDir))
	return func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(distDir, filepath.Clean(r.URL.Path))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(distDir, "index.html"))
	}
}
