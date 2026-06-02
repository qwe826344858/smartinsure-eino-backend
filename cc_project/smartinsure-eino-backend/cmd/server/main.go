package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"smartinsure-eino-backend/internal/api"
)

func main() {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":34567"
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           api.NewHandler(nil),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("SmartInsure Go API listening on %s", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
