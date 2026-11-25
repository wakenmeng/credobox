// Package controlplane is the Control Plane: the component behind the public
// API. It owns the Sandbox records (SQLite is the source of truth) and calls
// the Host Agent over gRPC to make them real.
package controlplane

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	hav1 "gpubox/gen/hostagent/v1"
)

// Config configures a Control Plane.
type Config struct {
	// Addr is the TCP address of the public REST API.
	Addr   string
	DBPath string
	// HostAgentSocket is the one Host's unix socket in phase 1.
	HostAgentSocket string
	// CreateTimeout bounds POST /v1/sandboxes. A Sandbox not Running by
	// then becomes Terminated with reason start_timeout.
	CreateTimeout time.Duration
}

// ControlPlane is a running Control Plane.
type ControlPlane struct {
	cfg   Config
	store *store
	conn  *grpc.ClientConn
	host  hav1.HostAgentClient
	http  *http.Server
	addr  string
}

// Start opens the database, connects to the Host Agent and serves the API.
func Start(cfg Config) (*ControlPlane, error) {
	if cfg.CreateTimeout == 0 {
		cfg.CreateTimeout = 60 * time.Second
	}
	st, err := openStore(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient("unix://"+cfg.HostAgentSocket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		st.close()
		return nil, err
	}
	lis, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		conn.Close()
		st.close()
		return nil, err
	}
	cp := &ControlPlane{cfg: cfg, store: st, conn: conn, host: hav1.NewHostAgentClient(conn), addr: lis.Addr().String()}
	cp.http = &http.Server{Handler: cp.routes(), ReadHeaderTimeout: 10 * time.Second}
	go cp.http.Serve(lis)
	return cp, nil
}

// Addr is the address the REST API listens on.
func (cp *ControlPlane) Addr() string { return cp.addr }

// Stop stops serving and closes the database.
func (cp *ControlPlane) Stop() {
	cp.http.Close()
	cp.conn.Close()
	cp.store.close()
}

func (cp *ControlPlane) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sandboxes", cp.handleCreate)
	mux.HandleFunc("GET /v1/sandboxes/{id}", cp.handleGet)
	mux.HandleFunc("DELETE /v1/sandboxes/{id}", cp.handleDelete)
	return mux
}

// sandboxJSON is the public API's Sandbox object.
type sandboxJSON struct {
	ID                string            `json:"id"`
	Template          string            `json:"template"`
	State             SandboxState      `json:"state"`
	TerminationReason TerminationReason `json:"termination_reason,omitempty"`
	TerminationDetail string            `json:"termination_detail,omitempty"`
	DataEndpoint      string            `json:"data_endpoint,omitempty"`
	// AccessToken is returned once, by create.
	AccessToken string    `json:"access_token,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

func toJSON(sb *Sandbox) sandboxJSON {
	return sandboxJSON{
		ID: sb.ID, Template: sb.Template, State: sb.State,
		TerminationReason: sb.TerminationReason, TerminationDetail: sb.TerminationDetail,
		DataEndpoint: sb.DataEndpoint, CreatedAt: sb.CreatedAt.UTC(),
	}
}

type createRequest struct {
	Template string `json:"template"`
}

// handleCreate blocks until the Sandbox is Running, or Terminated.
func (cp *ControlPlane) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil || req.Template == "" {
		writeError(w, http.StatusBadRequest, "body must be JSON with a non-empty \"template\"")
		return
	}
	accessToken := newAccessToken()
	hash := sha256.Sum256([]byte(accessToken))
	now := time.Now()
	sb := &Sandbox{
		ID: newSandboxID(), TenantID: DefaultTenantID, Template: req.Template,
		State: StateStarting, AccessTokenHash: hash[:], CreatedAt: now, UpdatedAt: now,
	}
	// The record exists, as Starting, before the Host is asked for anything,
	// so reconciliation (ticket 10) can always account for the container.
	if err := cp.store.insert(r.Context(), sb); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), cp.cfg.CreateTimeout)
	defer cancel()
	res, err := cp.host.CreateSandbox(ctx, &hav1.CreateSandboxRequest{
		SandboxId: sb.ID, TemplateImage: sb.Template, AccessToken: accessToken,
	})
	if err != nil {
		cp.failCreate(w, sb, ctx, err)
		return
	}
	if err := cp.store.markRunning(context.WithoutCancel(r.Context()), sb.ID, res.DataEndpoint); err != nil {
		// The client never gets the Access Token, so nobody can use this
		// Sandbox: remove it now rather than leave it running. The record
		// stays Starting, which reconciliation (ticket 10) settles.
		bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, derr := cp.host.DestroySandbox(bg, &hav1.DestroySandboxRequest{SandboxId: sb.ID}); derr != nil {
			log.Printf("controlplane: destroying sandbox %s after a failed write: %v", sb.ID, derr)
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sb.State, sb.DataEndpoint = StateRunning, res.DataEndpoint
	out := toJSON(sb)
	out.AccessToken = accessToken
	writeJSON(w, http.StatusCreated, out)
}

// failCreate records why creation failed, makes sure the Host has nothing
// left over, and answers with the Terminated Sandbox.
func (cp *ControlPlane) failCreate(w http.ResponseWriter, sb *Sandbox, ctx context.Context, err error) {
	reason, code := ReasonFailed, http.StatusBadGateway
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded {
		reason, code = ReasonStartTimeout, http.StatusGatewayTimeout
	}
	detail := status.Convert(err).Message()
	if reason == ReasonStartTimeout {
		detail = fmt.Sprintf("not Running within %s: %s", cp.cfg.CreateTimeout, detail)
	}

	bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, derr := cp.host.DestroySandbox(bg, &hav1.DestroySandboxRequest{SandboxId: sb.ID}); derr != nil {
		log.Printf("controlplane: destroying failed sandbox %s: %v", sb.ID, derr)
	}
	if serr := cp.store.markTerminated(bg, sb.ID, reason, detail); serr != nil {
		writeError(w, http.StatusInternalServerError, serr.Error())
		return
	}
	sb.State, sb.TerminationReason, sb.TerminationDetail = StateTerminated, reason, detail
	writeJSON(w, code, toJSON(sb))
}

func (cp *ControlPlane) handleGet(w http.ResponseWriter, r *http.Request) {
	sb, err := cp.store.get(r.Context(), r.PathValue("id"))
	if errors.Is(err, errNoSandbox) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toJSON(sb))
}

func (cp *ControlPlane) handleDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sb, err := cp.store.get(ctx, r.PathValue("id"))
	if errors.Is(err, errNoSandbox) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sb.State != StateTerminated {
		if _, err := cp.host.DestroySandbox(ctx, &hav1.DestroySandboxRequest{SandboxId: sb.ID}); err != nil {
			writeError(w, http.StatusBadGateway, "host agent: "+status.Convert(err).Message())
			return
		}
		if err := cp.store.markTerminated(ctx, sb.ID, ReasonDeleted, ""); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		sb.State, sb.TerminationReason = StateTerminated, ReasonDeleted
	}
	writeJSON(w, http.StatusOK, toJSON(sb))
}

func newSandboxID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "sbx_" + hex.EncodeToString(b)
}

func newAccessToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return "sat_" + base64.RawURLEncoding.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
