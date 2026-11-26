package guestagent

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func etcWith(t *testing.T, passwd, group string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"passwd": passwd, "group": group} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ADR 0009: an image without `user` gets one, with the first free uid from 1000.
func TestAddUserAccountTakesTheFirstFreeIDsFrom1000(t *testing.T) {
	for _, tc := range []struct {
		name          string
		passwd, group string
		uid, gid      uint32
	}{
		{"alpine-like, nothing at 1000",
			"root:x:0:0:root:/root:/bin/sh\nnobody:x:65534:65534:nobody:/:/sbin/nologin\n",
			"root:x:0:root\nnobody:x:65534:\n", 1000, 1000},
		{"ubuntu-like, 1000 is taken",
			"root:x:0:0:root:/root:/bin/bash\nubuntu:x:1000:1000:Ubuntu:/home/ubuntu:/bin/bash\n",
			"root:x:0:\nubuntu:x:1000:\n", 1001, 1001},
		{"uid free but gid 1000 taken",
			"root:x:0:0:root:/root:/bin/sh\n",
			"root:x:0:\nshared:x:1000:\n", 1000, 1001},
		{"no trailing newline",
			"root:x:0:0:root:/root:/bin/sh",
			"root:x:0:", 1000, 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			etc := etcWith(t, tc.passwd, tc.group)

			created, err := addUserAccount(etc)
			if err != nil || !created {
				t.Fatalf("addUserAccount: created=%v, %v; want created", created, err)
			}

			id, err := resolveUser("user", etc)
			if err != nil {
				t.Fatalf("resolve the new account: %v", err)
			}
			if id.cred.Uid != tc.uid || id.cred.Gid != tc.gid || id.home != "/home/user" ||
				!reflect.DeepEqual(id.cred.Groups, []uint32{tc.gid}) {
				t.Fatalf("got uid=%d gid=%d groups=%v home=%q, want %d %d [%d] /home/user",
					id.cred.Uid, id.cred.Gid, id.cred.Groups, id.home, tc.uid, tc.gid, tc.gid)
			}
			// The image's own lines are untouched; only ours is added.
			if got := readFile(t, filepath.Join(etc, "passwd")); len(got) <= len(tc.passwd) || got[:len(tc.passwd)] != tc.passwd {
				t.Fatalf("passwd lost its original content: %q", got)
			}
		})
	}
}

func TestAddUserAccountLeavesAnExistingUserAlone(t *testing.T) {
	passwd := "root:x:0:0:root:/root:/bin/sh\nuser:x:2000:2000::/home/user:/bin/bash\n"
	group := "root:x:0:\nuser:x:2000:\n"
	etc := etcWith(t, passwd, group)

	created, err := addUserAccount(etc)

	if err != nil || created {
		t.Fatalf("created=%v, %v; want an existing user left as it is", created, err)
	}
	if readFile(t, filepath.Join(etc, "passwd")) != passwd || readFile(t, filepath.Join(etc, "group")) != group {
		t.Fatal("passwd or group changed")
	}
}

func TestAddUserAccountReusesAnExistingUserGroup(t *testing.T) {
	etc := etcWith(t, "root:x:0:0:root:/root:/bin/sh\n", "root:x:0:\nuser:x:1500:\n")

	if _, err := addUserAccount(etc); err != nil {
		t.Fatal(err)
	}

	id, err := resolveUser("user", etc)
	if err != nil || id.cred.Uid != 1000 || id.cred.Gid != 1500 {
		t.Fatalf("got %+v, %v; want uid 1000 in the existing group 1500", id.cred, err)
	}
	if got := readFile(t, filepath.Join(etc, "group")); got != "root:x:0:\nuser:x:1500:\n" {
		t.Fatalf("group file changed: %q", got)
	}
}
