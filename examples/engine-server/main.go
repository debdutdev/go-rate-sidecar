package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/12345debdut/rate-limiter/engine"
)

func main() {
	// Two-line setup: create engine from YAML, wrap your handler.
	eng, err := engine.New("ratelimiter.yaml")
	if err != nil {
		log.Fatal(err)
	}
	defer eng.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/login", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "abc123"})
	})
	mux.HandleFunc("/api/v1/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"user": r.PathValue("id")})
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	handler := eng.Middleware()(mux)

	addr := ":8080"
	fmt.Printf("Engine-driven server on %s\n", addr)
	fmt.Println("Routes configured in ratelimiter.yaml — each with its own algorithm and limits")
	log.Fatal(http.ListenAndServe(addr, handler))
}
