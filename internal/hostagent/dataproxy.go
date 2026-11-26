package hostagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	gav1 "gpubox/gen/guestagent/v1"
	"gpubox/internal/envvars"
)

// Exec limits (spec §4).
const (
	DefaultExecTimeout = 60 * time.Second
	MaxExecTimeout     = time.Hour
	// BufferedOutputCap is how much of stdout, and of stderr, a buffered
	// (non-streaming) Exec returns. A streamed Exec has no cap.
	BufferedOutputCap = 1 << 20
)

// dataProxy is the Host data proxy (ADR 0007).
func (h *HostAgent) dataProxy() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sandboxes/{id}/exec", h.handleExec)
	return mux
}

type execRequest struct {
	Command string            `json:"command"`
	User    string            `json:"user"`
	Cwd     string            `json:"cwd"`
	Env     map[string]string `json:"env"`
	// 0 or absent means DefaultExecTimeout.
	TimeoutSeconds int `json:"timeout_seconds"`
}

type execResponse struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int32  `json:"exit_code"`
	// The timeout passed and the Exec's process group was killed.
	TimedOut bool `json:"timed_out"`
	// Each stream keeps its first 1 MiB; these say whether more was dropped.
	StdoutTruncated bool `json:"stdout_truncated"`
	StderrTruncated bool `json:"stderr_truncated"`
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
	timeout := DefaultExecTimeout
	if req.TimeoutSeconds != 0 {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}
	if timeout <= 0 || timeout > MaxExecTimeout {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("timeout_seconds must be between 1 and %d; use a Process for longer runs", int(MaxExecTimeout/time.Second)))
		return
	}
	if err := envvars.Validate(req.Env); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	streaming := strings.Contains(r.Header.Get("Accept"), "text/event-stream")
	greq := &gav1.ExecRequest{
		Command: req.Command, User: req.User, Cwd: req.Cwd, Env: req.Env,
		TimeoutMs: timeout.Milliseconds(),
	}
	if !streaming {
		greq.MaxOutputBytes = BufferedOutputCap
	}

	// r.Context() ends when the client disconnects. That cancels the call,
	// and the Guest Agent kills the Exec's process group. The Guest Agent
	// enforces the timeout itself; this deadline only backs it up.
	ctx, cancel := context.WithTimeout(r.Context(), timeout+10*time.Second)
	defer cancel()
	stream, err := sb.guest.Exec(sb.guestContext(ctx), greq)
	if err == nil {
		// The first event says the command started, or why it couldn't.
		var first *gav1.ExecEvent
		if first, err = stream.Recv(); err == nil && first.GetStarted() == nil {
			err = fmt.Errorf("guest agent sent %v before Started", first)
		}
		if err == nil {
			if streaming {
				streamExec(w, stream, first.GetStarted())
			} else {
				bufferExec(w, stream)
			}
			return
		}
	}
	if status.Code(err) == codes.InvalidArgument {
		writeError(w, http.StatusBadRequest, status.Convert(err).Message())
		return
	}
	writeError(w, http.StatusBadGateway, "guest agent: "+err.Error())
}

type execEvents interface {
	Recv() (*gav1.ExecEvent, error)
}

// bufferExec collects the whole Exec and answers with one JSON object.
func bufferExec(w http.ResponseWriter, stream execEvents) {
	var stdout, stderr []byte
	for {
		ev, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				err = errors.New("stream ended before the command did")
			}
			writeError(w, http.StatusBadGateway, "guest agent: "+err.Error())
			return
		}
		switch e := ev.Event.(type) {
		case *gav1.ExecEvent_Stdout:
			stdout = append(stdout, e.Stdout...)
		case *gav1.ExecEvent_Stderr:
			stderr = append(stderr, e.Stderr...)
		case *gav1.ExecEvent_Exited:
			x := e.Exited
			writeJSON(w, http.StatusOK, execResponse{
				Stdout: string(stdout), Stderr: string(stderr), ExitCode: x.ExitCode, TimedOut: x.TimedOut,
				StdoutTruncated: x.StdoutTruncated, StderrTruncated: x.StderrTruncated,
			})
			return
		}
	}
}

// streamExec relays the Exec as Server-Sent Events: `start`, then `stdout`
// and `stderr` as output is produced, then `exit` with the exit code (or
// `error` if the Guest Agent fails midway). Each event is flushed at once.
func streamExec(w http.ResponseWriter, stream execEvents, started *gav1.Started) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	send := func(name string, data any) error {
		b, _ := json.Marshal(data)
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b); err != nil {
			return err
		}
		return rc.Flush()
	}
	if send("start", map[string]int32{"pid": started.Pid}) != nil {
		return
	}
	// Output is read in chunks that can end inside a multi-byte character;
	// hold such a tail back until the rest of it arrives.
	var pending [2][]byte
	text := func(i int, b []byte) error {
		names := [2]string{"stdout", "stderr"}
		whole, rest := splitUTF8(append(pending[i], b...))
		pending[i] = rest
		if len(whole) == 0 {
			return nil
		}
		return send(names[i], map[string]string{"text": string(whole)})
	}
	for {
		ev, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				err = errors.New("stream ended before the command did")
			}
			send("error", map[string]string{"error": "guest agent: " + status.Convert(err).Message()})
			return
		}
		switch e := ev.Event.(type) {
		case *gav1.ExecEvent_Stdout:
			err = text(0, e.Stdout)
		case *gav1.ExecEvent_Stderr:
			err = text(1, e.Stderr)
		case *gav1.ExecEvent_Exited:
			for i, rest := range pending {
				if len(rest) > 0 {
					send([2]string{"stdout", "stderr"}[i], map[string]string{"text": string(rest)})
				}
			}
			send("exit", map[string]any{"exit_code": e.Exited.ExitCode, "timed_out": e.Exited.TimedOut})
			return
		}
		if err != nil {
			// The client is gone; returning cancels the Exec.
			return
		}
	}
}

// splitUTF8 splits b before an incomplete multi-byte character at its end.
func splitUTF8(b []byte) (whole, rest []byte) {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax+1; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return b[:i], b[i:]
			}
			break
		}
	}
	return b, nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
