package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync/atomic"
)

func main() {
	var success, flaky, permanent atomic.Int64
	m := http.NewServeMux()
	m.HandleFunc("/success", func(w http.ResponseWriter, r *http.Request) { success.Add(1); w.WriteHeader(204) })
	m.HandleFunc("/flaky", func(w http.ResponseWriter, r *http.Request) {
		n := flaky.Add(1)
		if n == 1 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(204)
	})
	m.HandleFunc("/permanent", func(w http.ResponseWriter, r *http.Request) { permanent.Add(1); w.WriteHeader(400) })
	m.HandleFunc("/counts", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int64{"success": success.Load(), "flaky": flaky.Load(), "permanent": permanent.Load()})
	})
	m.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	log.Fatal(http.ListenAndServe(":8081", m))
}
