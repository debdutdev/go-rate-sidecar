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

	"github.com/12345debdut/rate-limiter/engine"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	configPath := flag.String("config", "ratelimiter.yaml", "path to config file")
	flag.Parse()

	eng, err := engine.New(*configPath)
	if err != nil {
		log.Fatalf("failed to create engine: %v", err)
	}
	defer eng.Close()

	handler, err := eng.ProxyHandler()
	if err != nil {
		log.Fatalf("failed to create proxy handler: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/__metrics", promhttp.Handler())
	mux.Handle("/", handler)

	addr := eng.Config().Server.Listen
	if addr == "" {
		addr = ":8080"
	}

	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		log.Printf("received %v, shutting down...", sig)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("shutdown error: %v", err)
		}
	}()

	log.Printf("rate limiter proxy listening on %s, forwarding to %s",
		addr, eng.Config().Upstream.URL)

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
	log.Println("server stopped")
}
