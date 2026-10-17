// pms-dummy is a throwaway cloud-PMS polygon for testing the servr.ai PMS
// webhook integration (https://api-dev.servr.ai/docs/guides/pms-webhook.html).
//
// It receives servr's signed action deliveries, serves reservation reads,
// sends signed events back to servr, and exposes a JavaScript-free web UI
// for rooms/reservations management, webhook configuration, fault injection,
// sync statuses and activity logs.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/servrai/pms-dummy/internal/store"
	"github.com/servrai/pms-dummy/internal/web"
)

func main() {
	addr := flag.String("addr", ":80", "listen address")
	dbPath := flag.String("db", "/data/pms-dummy.db", "SQLite database path")
	flag.Parse()

	ownBase := os.Getenv("PMS_DUMMY_OWN_BASE_URL")

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	srv := web.New(st, ownBase)
	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("pms-dummy listening on %s (db %s, own base %q)", *addr, *dbPath, ownBase)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
}
