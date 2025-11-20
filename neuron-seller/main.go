package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

// -----------------------------
// Config
// -----------------------------

type SellerConfig struct {
	SellerID string  `json:"seller_id"`
	PiBase   string  `json:"pi_base"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	Label    string  `json:"label"`
	Port     string  `json:"-"`
}

var sellerCfg SellerConfig

func mustGetEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("missing required env var %s", key)
	}
	return v
}

func mustParseFloat(s string, name string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		log.Fatalf("invalid %s '%s': %v", name, s, err)
	}
	return f
}

func loadConfig() {
	sellerID := mustGetEnv("SELLER_ID")
	piBase := mustGetEnv("PI_BASE_URL")
	latStr := mustGetEnv("SELLER_LAT")
	lonStr := mustGetEnv("SELLER_LON")
	label := mustGetEnv("SELLER_LABEL")

	port := os.Getenv("SELLER_PORT")
	if port == "" {
		port = "9000"
	}

	sellerCfg = SellerConfig{
		SellerID: sellerID,
		PiBase:   piBase,
		Lat:      mustParseFloat(latStr, "SELLER_LAT"),
		Lon:      mustParseFloat(lonStr, "SELLER_LON"),
		Label:    label,
		Port:     port,
	}

	log.Printf("=== LocalSense Neuron Seller Shim (Pi) ===")
	log.Printf("SellerID  : %s", sellerCfg.SellerID)
	log.Printf("PiBase    : %s", sellerCfg.PiBase)
	log.Printf("Location  : (%f, %f) label=%s", sellerCfg.Lat, sellerCfg.Lon, sellerCfg.Label)
	log.Printf("HTTP seller shim listening on :%s", sellerCfg.Port)
}

// -----------------------------
// Helpers to call Pi service
// -----------------------------

func fetchJSON(url string, dest any) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %s", url, resp.Status)
	}

	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

// -----------------------------
// HTTP Handlers
// -----------------------------

// Simple text help on /health (what you already saw)
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "LocalSense Neuron Seller Shim")
	fmt.Fprintln(w, "Endpoints:")
	fmt.Fprintln(w, "  GET /status – one-shot status (config + Pi metrics + Pi health)")
	fmt.Fprintln(w, "  GET /stream – NDJSON stream of brightness samples")
}

// One-shot status, now includes Pi /metrics and /health
func statusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	now := time.Now().UTC().Format(time.RFC3339)

	piMetrics := make(map[string]any)
	piHealth := make(map[string]any)

	// Try to fetch Pi metrics and health; if they fail, we just log and omit them.
	if err := fetchJSON(sellerCfg.PiBase+"/metrics", &piMetrics); err != nil {
		log.Printf("[/status] error fetching /metrics from Pi: %v", err)
		piMetrics = nil
	}
	if err := fetchJSON(sellerCfg.PiBase+"/health", &piHealth); err != nil {
		log.Printf("[/status] error fetching /health from Pi: %v", err)
		piHealth = nil
	}

	resp := map[string]any{
		"config":   sellerCfg,
		"time_iso": now,
	}

	if piMetrics != nil {
		resp["pi_metrics"] = piMetrics
	}
	if piHealth != nil {
		resp["pi_health"] = piHealth
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("[/status] encode error: %v", err)
	}
}

// Streaming endpoint: emits brightness samples as NDJSON
func streamHandler(w http.ResponseWriter, r *http.Request) {
	// NDJSON = one JSON object per line
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	log.Printf("[/stream] client connected from %s", r.RemoteAddr)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	enc := json.NewEncoder(w)

	for {
		select {
		case <-r.Context().Done():
			log.Printf("[/stream] client disconnected from %s", r.RemoteAddr)
			return

		case t := <-ticker.C:
			piMetrics := make(map[string]any)
			if err := fetchJSON(sellerCfg.PiBase+"/metrics", &piMetrics); err != nil {
				log.Printf("[/stream] error fetching /metrics from Pi: %v", err)
				continue
			}

			payload := map[string]any{
				"ts":         piMetrics["ts"],
				"brightness": piMetrics["brightness"],
				"seller_id":  sellerCfg.SellerID,
				"lat":        sellerCfg.Lat,
				"lon":        sellerCfg.Lon,
				"label":      sellerCfg.Label,
				"time_iso":   t.UTC().Format(time.RFC3339),
			}

			if err := enc.Encode(payload); err != nil {
				log.Printf("[/stream] encode error: %v", err)
				return
			}

			flusher.Flush()
		}
	}
}

// -----------------------------
// main
// -----------------------------

func main() {
	loadConfig()

	server := buildHTTPServer()

	if neuronStreamingEnabled() {
		log.Printf("Neuron seller mode enabled; exposing shim on %s and starting Neuron SDK", server.Addr)
		go func() {
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("HTTP server error: %v", err)
			}
		}()

		if err := runNeuronSellerNode(); err != nil {
			log.Fatalf("Neuron seller exited with error: %v", err)
		}
		return
	}

	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("ListenAndServe: %v", err)
	}
}

func buildHTTPServer() *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/status", statusHandler)
	mux.HandleFunc("/stream", streamHandler)

	return &http.Server{
		Addr:    ":" + sellerCfg.Port,
		Handler: mux,
	}
}
