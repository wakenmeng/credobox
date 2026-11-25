package guestagent

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeEtc(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"passwd": "root:x:0:0:root:/root:/bin/sh\napp:x:1234:1234::/home/app:/bin/sh\n",
		"group":  "root:x:0:root\nwheel:x:10:root\napp:x:1234:\nstaff:x:50:app,other\ndocker:x:999:app\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestResolveUserLikeDocker(t *testing.T) {
	etc := writeEtc(t)
	for _, tc := range []struct {
		spec     string
		uid, gid uint32
		groups   []uint32
		home     string
	}{
		{"app", 1234, 1234, []uint32{1234, 50, 999}, "/home/app"},
		{"1234", 1234, 1234, []uint32{1234, 50, 999}, "/home/app"},
		{"app:staff", 1234, 50, []uint32{50, 999}, "/home/app"},
		{"app:42", 1234, 42, []uint32{42, 50, 999}, "/home/app"},
		{"4321", 4321, 0, []uint32{0}, "/"},
		{"4321:4321", 4321, 4321, []uint32{4321}, "/"},
	} {
		id, err := resolveUser(tc.spec, etc)
		if err != nil {
			t.Errorf("%q: %v", tc.spec, err)
			continue
		}
		c := id.cred
		if c.Uid != tc.uid || c.Gid != tc.gid || !reflect.DeepEqual(c.Groups, tc.groups) || id.home != tc.home {
			t.Errorf("%q: got uid=%d gid=%d groups=%v home=%q, want %d %d %v %q",
				tc.spec, c.Uid, c.Gid, c.Groups, id.home, tc.uid, tc.gid, tc.groups, tc.home)
		}
	}
}

func TestResolveUserEmptyMeansRoot(t *testing.T) {
	id, err := resolveUser("", writeEtc(t))
	if err != nil || id.cred != nil {
		t.Fatalf("got %+v, %v; want no credential switch", id, err)
	}
}

func TestResolveUserUnknownNameFails(t *testing.T) {
	etc := writeEtc(t)
	for _, spec := range []string{"nobody-here", "app:nogroup"} {
		if _, err := resolveUser(spec, etc); err == nil {
			t.Errorf("%q: resolved, want an error", spec)
		}
	}
}
