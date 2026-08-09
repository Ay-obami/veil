package typesserver

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ethereum/go-ethereum/common/hexutil"

	"veil/internal/config"
	"veil/pkg/decoder"
)

type decodeRequest struct {
	OPType    string `json:"opType"`
	OPCommand string `json:"opCommand"`
	Kind      string `json:"kind"`
	Data      string `json:"data"`
}

type decodeResponse struct {
	Decoded any `json:"decoded"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// Server holds the HTTP handlers and decoder registry.
type Server struct {
	registry *decoder.Registry
	mux      *http.ServeMux
}

// New creates a Server wired to the given registry.
func New(registry *decoder.Registry) *Server {
	s := &Server{registry: registry, mux: http.NewServeMux()}
	s.mux.HandleFunc("POST /decode", s.handleDecode)
	s.mux.HandleFunc("GET /registry", s.handleRegistry)
	s.mux.HandleFunc("GET /health", s.handleHealth)
	return s
}

// ListenAndServe starts the HTTP server on the given port.
func (s *Server) ListenAndServe(port int) error {
	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("types-server %s listening on %s\n", config.Version, addr)
	return http.ListenAndServe(addr, s.mux)
}

func (s *Server) handleDecode(w http.ResponseWriter, r *http.Request) {
	var req decodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body: " + err.Error()})
		return
	}

	kind := decoder.DataKind(req.Kind)
	if kind != decoder.KindMessage && kind != decoder.KindResult {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "kind must be \"message\" or \"result\""})
		return
	}

	data, err := hexutil.Decode(req.Data)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid hex data: " + err.Error()})
		return
	}

	dec, err := s.registry.Lookup(req.OPType, req.OPCommand, kind)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: err.Error()})
		return
	}

	decoded, err := dec.Decode(data)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, errorResponse{Error: "decode failed: " + err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, decodeResponse{Decoded: decoded})
}

func (s *Server) handleRegistry(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.registry.Keys())
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": config.Version})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}
