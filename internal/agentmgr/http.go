package agentmgr

import (
	"encoding/json"
	"log"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/google/uuid"
)

const maxBody = 1 << 20 // 1 MiB cap on daemon request bodies

// Handler returns the daemon-facing REST routes (LLD-08 §2.3). They authenticate
// with a bearer token (not cookies), so they are mounted OUTSIDE the GraphQL
// CSRF + session middleware: a machine daemon sends no Origin/cookie and would
// be rejected by those. No CSRF surface exists here (no ambient cookie auth).
func Handler(svc *Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agents/{vm_id}/enroll", svc.handleEnroll)
	mux.HandleFunc("POST /v1/agents/{vm_id}/heartbeat", svc.handleHeartbeat)
	// LLD-11 §6: knowledge-pack delivery over the same authenticated channel.
	mux.HandleFunc("GET /v1/agents/{vm_id}/knowledge", svc.handleKnowledgeList)
	mux.HandleFunc("GET /v1/agents/{vm_id}/knowledge/{artifact_id}", svc.handleKnowledgeGet)
	return recoverMiddleware(mux)
}

// recoverMiddleware mirrors the GraphQL plane's panic masking (graph.RecoverFunc):
// a panic in a daemon handler is logged with a stack + correlation id and
// returned as an opaque 500, so one bad request can't crash the server or leak
// internals — both planes fail uniformly.
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				id := uuid.NewString()
				log.Printf("agentmgr REST panic [%s] %s %s: %v\n%s", id, r.Method, r.URL.Path, p, debug.Stack())
				writeErr(w, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// bearer extracts the token from an "Authorization: Bearer <t>" header.
func bearer(r *http.Request) string {
	if t, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(t)
	}
	return ""
}

// handleEnroll exchanges a one-time enroll token for a long-lived VM token.
func (s *Service) handleEnroll(w http.ResponseWriter, r *http.Request) {
	vmID, tok := r.PathValue("vm_id"), bearer(r)
	if vmID == "" || tok == "" {
		writeErr(w, http.StatusUnauthorized)
		return
	}
	vmToken, err := s.Enroll(r.Context(), vmID, tok)
	if err != nil {
		writeErr(w, http.StatusUnauthorized) // opaque (fail-closed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"vm_token": vmToken})
}

// handleHeartbeat authenticates the VM token, records the heartbeat, and returns
// pending commands (+ optionally a renewed token).
func (s *Service) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	vmID, tok := r.PathValue("vm_id"), bearer(r)
	if vmID == "" || tok == "" {
		writeErr(w, http.StatusUnauthorized)
		return
	}
	enr, err := s.Authenticate(r.Context(), vmID, tok)
	if err != nil {
		writeErr(w, http.StatusUnauthorized) // opaque (fail-closed)
		return
	}
	var req HeartbeatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest)
		return
	}
	resp, err := s.ProcessHeartbeat(r.Context(), enr, req)
	if err != nil {
		writeErr(w, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr returns an opaque error body — auth failures never reveal the reason.
func writeErr(w http.ResponseWriter, code int) {
	writeJSON(w, code, map[string]string{"error": http.StatusText(code)})
}
