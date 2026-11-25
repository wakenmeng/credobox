package hostagent

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// shortDir returns a directory short enough for a unix socket path on macOS,
// which t.TempDir (named after the test) can exceed.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ha")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestStartReplacesAStaleSocket(t *testing.T) {
	path := filepath.Join(shortDir(t), "ctl.sock")
	// A socket file left behind by a Host Agent that died without cleaning up.
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	l.Close()

	h, err := Start(Config{SocketPath: path, DataAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("Start over a stale socket: %v", err)
	}
	defer h.Stop()
	fi, err := os.Stat(path)
	if err != nil || fi.Mode()&os.ModeSocket == 0 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("control socket: %v, %v; want a 0600 socket", fi.Mode(), err)
	}
}

func TestStartRefusesToRemoveSomethingThatIsNotASocket(t *testing.T) {
	path := filepath.Join(shortDir(t), "important.txt")
	if err := os.WriteFile(path, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	if h, err := Start(Config{SocketPath: path, DataAddr: "127.0.0.1:0"}); err == nil {
		h.Stop()
		t.Fatal("Start succeeded over a regular file, want an error")
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "keep me" {
		t.Fatalf("the file at the socket path was changed: %q, %v", b, err)
	}
}
