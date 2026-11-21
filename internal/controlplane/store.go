package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// SandboxState is a Sandbox's lifecycle state (GLOSSARY, spec §5).
type SandboxState string

const (
	StateStarting   SandboxState = "Starting"
	StateRunning    SandboxState = "Running"
	StateTerminated SandboxState = "Terminated"
)

// TerminationReason says why a Sandbox became Terminated (spec §5).
type TerminationReason string

const (
	ReasonDeleted      TerminationReason = "deleted"
	ReasonFailed       TerminationReason = "failed"
	ReasonStartTimeout TerminationReason = "start_timeout"
)

// DefaultTenantID is the single hard-coded Tenant until ticket 02.
const DefaultTenantID = "tenant_default"

// Sandbox is the Control Plane's record of a Sandbox. The Control Plane is
// the source of truth; it never stores the Access Token itself, only its hash.
type Sandbox struct {
	ID                string
	TenantID          string
	Template          string
	State             SandboxState
	TerminationReason TerminationReason
	TerminationDetail string
	DataEndpoint      string
	AccessTokenHash   []byte
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

var errNoSandbox = errors.New("no such sandbox")

type store struct{ db *sql.DB }

func openStore(path string) (*store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	// One connection: SQLite has a single writer anyway (spec §6).
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS sandboxes (
		id                 TEXT PRIMARY KEY,
		tenant_id          TEXT NOT NULL,
		template           TEXT NOT NULL,
		state              TEXT NOT NULL,
		termination_reason TEXT NOT NULL DEFAULT '',
		termination_detail TEXT NOT NULL DEFAULT '',
		data_endpoint      TEXT NOT NULL DEFAULT '',
		access_token_hash  BLOB NOT NULL,
		created_at         INTEGER NOT NULL,
		updated_at         INTEGER NOT NULL
	)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &store{db: db}, nil
}

func (s *store) close() error { return s.db.Close() }

func (s *store) insert(ctx context.Context, sb *Sandbox) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sandboxes
		(id, tenant_id, template, state, access_token_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sb.ID, sb.TenantID, sb.Template, sb.State, sb.AccessTokenHash, sb.CreatedAt.UnixNano(), sb.UpdatedAt.UnixNano())
	return err
}

func (s *store) markRunning(ctx context.Context, id, dataEndpoint string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sandboxes SET state = ?, data_endpoint = ?, updated_at = ? WHERE id = ?`,
		StateRunning, dataEndpoint, time.Now().UnixNano(), id)
	return err
}

func (s *store) markTerminated(ctx context.Context, id string, reason TerminationReason, detail string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sandboxes SET state = ?, termination_reason = ?, termination_detail = ?, updated_at = ? WHERE id = ?`,
		StateTerminated, reason, detail, time.Now().UnixNano(), id)
	return err
}

func (s *store) get(ctx context.Context, id string) (*Sandbox, error) {
	var sb Sandbox
	var created, updated int64
	err := s.db.QueryRowContext(ctx, `SELECT id, tenant_id, template, state, termination_reason, termination_detail,
		data_endpoint, access_token_hash, created_at, updated_at FROM sandboxes WHERE id = ?`, id).
		Scan(&sb.ID, &sb.TenantID, &sb.Template, &sb.State, &sb.TerminationReason, &sb.TerminationDetail,
			&sb.DataEndpoint, &sb.AccessTokenHash, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNoSandbox
	}
	if err != nil {
		return nil, err
	}
	sb.CreatedAt, sb.UpdatedAt = time.Unix(0, created), time.Unix(0, updated)
	return &sb, nil
}
