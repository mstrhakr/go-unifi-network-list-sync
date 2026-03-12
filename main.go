package main

import (
	"context"
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/ferventgeek/go-unifi-network-list-sync/internal/scheduler"
	"github.com/ferventgeek/go-unifi-network-list-sync/internal/store"
	"github.com/ferventgeek/go-unifi-network-list-sync/internal/syncer"
	"github.com/ferventgeek/go-unifi-network-list-sync/internal/web"
)

//go:embed ui
var uiContent embed.FS

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	dbPath := flag.String("db", "sync.db", "SQLite database path")
	flag.Parse()

	db, err := store.New(*dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	syn := syncer.New()

	sched := scheduler.New(db, syn)
	if err := sched.Start(); err != nil {
		log.Fatalf("Failed to start scheduler: %v", err)
	}
	defer sched.Stop()

	uiFS, err := fs.Sub(uiContent, "ui")
	if err != nil {
		log.Fatalf("Failed to load embedded UI: %v", err)
	}

	handler := web.NewHandler(db, syn, sched, uiFS)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("Server starting on %s", *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)
	<-quit

	log.Println("Shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Shutdown error: %v", err)
	}
}
