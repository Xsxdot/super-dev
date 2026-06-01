// Package main provides the Go HTTP pipeline example.
//
// Responsibilities:
//   - Expose /health for deployment health checks.
//   - Expose /info with language and app metadata.
//
// Boundaries:
//   - Does not read external configuration files.
//   - Does not depend on databases, queues, or reverse proxies.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "18080"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/info", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"app":      "go-http",
			"language": "go",
			"version":  os.Getenv("APP_VERSION"),
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("go-http"))
	})
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
