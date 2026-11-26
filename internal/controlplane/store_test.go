package controlplane

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// A Sandbox's env is likely to hold credentials. Only a live Sandbox needs it
// (for a re-Init), so a Terminated one must not keep it.
func TestTerminatedSandboxNoLongerHoldsItsEnvironment(t *testing.T) {
	st, err := openStore(filepath.Join(t.TempDir(), "cp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	ctx := context.Background()
	now := time.Now()
	sb := &Sandbox{
		ID: "sbx_test", TenantID: DefaultTenantID, Template: "alpine", State: StateStarting,
		DefaultEnv: map[string]string{"OPENAI_API_KEY": "sk-live"}, AccessTokenHash: []byte{1}, CreatedAt: now, UpdatedAt: now,
	}
	if err := st.insert(ctx, sb); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.get(ctx, sb.ID); got.DefaultEnv["OPENAI_API_KEY"] != "sk-live" {
		t.Fatalf("live Sandbox env = %v, want it kept for re-Init", got.DefaultEnv)
	}

	if err := st.markTerminated(ctx, sb.ID, ReasonDeleted, ""); err != nil {
		t.Fatal(err)
	}

	got, err := st.get(ctx, sb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.DefaultEnv) != 0 {
		t.Fatalf("Terminated Sandbox still holds env %v", got.DefaultEnv)
	}
	var raw string
	st.db.QueryRow(`SELECT default_env FROM sandboxes WHERE id = ?`, sb.ID).Scan(&raw)
	if raw != "{}" {
		t.Fatalf("default_env column = %q, want {}", raw)
	}
}
