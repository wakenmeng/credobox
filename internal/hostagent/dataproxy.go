package hostagent

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	gav1 "gpubox/gen/guestagent/v1"
)

// dataProxy is the Host data proxy (ADR 0007).
func (h *HostAgent) dataProxy() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sandboxes/{id}/exec", h.handleExec)
	return mux
}

type execRequest struct {
	Command string `json:"command"`
}

type execResponse struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int32  `json:"exit_code"`
}

func (h *HostAgent) handleExec(w http.ResponseWriter, r *http.Request) {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	sb, err := h.lookup(r.PathValue("id"), token)
	switch {
	case errors.Is(err, errNotFound):
		writeError(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	var req execRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil || req.Command == "" {
		writeError(w, http.StatusBadRequest, "body must be JSON with a non-empty \"command\"")
		return
	}
	// r.Context() ends when the client disconnects, which cancels the Exec.
	res, err := sb.guest.Exec(sb.guestContext(r.Context()), &gav1.ExecRequest{Command: req.Command})
	if err != nil {
		writeError(w, http.StatusBadGateway, "guest agent: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, execResponse{Stdout: string(res.Stdout), Stderr: string(res.Stderr), ExitCode: res.ExitCode})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
