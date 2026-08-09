// cors-proxy is a lightweight reverse proxy that adds CORS headers to the
// TEE extension proxy, enabling browser-based frontends to call /direct,
// /state, and /action/* endpoints.
//
// Usage:
//
//	go run ./cmd/cors-proxy --target http://localhost:6664 --listen :6670 --allow-origin http://localhost:5173
//
// Note: Chrome blocks ports 6665-6669 as "unsafe" (IRC range), so we listen on
// 6670 by default — browsers will refuse to connect to 6665.
package main

import (
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

func main() {
	target := flag.String("target", "http://localhost:6664", "upstream TEE proxy URL")
	listen := flag.String("listen", ":6670", "address to listen on")
	allowOrigin := flag.String("allow-origin", "*", "Access-Control-Allow-Origin value")
	flag.Parse()

	upstream, err := url.Parse(*target)
	if err != nil {
		log.Fatalf("invalid target URL: %s", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(upstream)

	// Preserve the original director and override Host.
	origDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		origDirector(req)
		req.Host = upstream.Host
	}

	handler := corsMiddleware(proxy, *allowOrigin)

	log.Printf("cors-proxy listening on %s → %s (allow-origin: %s)", *listen, *target, *allowOrigin)
	if err := http.ListenAndServe(*listen, handler); err != nil {
		log.Fatalf("server error: %s", err)
	}
}

func corsMiddleware(next http.Handler, allowOrigin string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && originAllowed(origin, allowOrigin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func originAllowed(origin, allowed string) bool {
	if allowed == "*" {
		return true
	}
	for _, a := range strings.Split(allowed, ",") {
		if strings.TrimSpace(a) == origin {
			return true
		}
	}
	return false
}
