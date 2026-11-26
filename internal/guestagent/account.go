package guestagent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// The ordinary account Execs run as by default (ADR 0009).
const (
	DefaultUser = "user"
	userHome    = "/home/user"
	firstUID    = 1000
)

// addUserAccount adds the `user` account to the passwd and group files in
// etcDir unless passwd already has one (ADR 0009). It writes the files itself,
// because which of adduser and useradd an image has depends on its
// distribution. The new account takes the first uid from 1000 up that passwd
// doesn't use, and as gid either an existing `user` group, the same number if
// it is free, or the first free gid from 1000 up. It reports whether it added
// anything.
func addUserAccount(etcDir string) (bool, error) {
	passwd, err := readEtc(filepath.Join(etcDir, "passwd"))
	if err != nil {
		return false, err
	}
	usedUIDs := map[int]bool{}
	for _, f := range passwd {
		if f[0] == DefaultUser {
			return false, nil
		}
		if len(f) >= 3 {
			if n, err := strconv.Atoi(f[2]); err == nil {
				usedUIDs[n] = true
			}
		}
	}
	groups, err := readEtc(filepath.Join(etcDir, "group"))
	if err != nil {
		return false, err
	}
	usedGIDs := map[int]bool{}
	gid := -1
	for _, f := range groups {
		if len(f) < 3 {
			continue
		}
		n, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		usedGIDs[n] = true
		if f[0] == DefaultUser {
			gid = n
		}
	}

	uid := firstFree(usedUIDs)
	if gid < 0 {
		if gid = uid; usedGIDs[gid] {
			gid = firstFree(usedGIDs)
		}
		if err := appendLine(filepath.Join(etcDir, "group"), fmt.Sprintf("%s:x:%d:", DefaultUser, gid)); err != nil {
			return false, err
		}
	}
	line := fmt.Sprintf("%s:x:%d:%d::%s:/bin/sh", DefaultUser, uid, gid, userHome)
	if err := appendLine(filepath.Join(etcDir, "passwd"), line); err != nil {
		return false, err
	}
	return true, nil
}

func firstFree(used map[int]bool) int {
	n := firstUID
	for used[n] {
		n++
	}
	return n
}

// appendLine adds line to the file, first ending its last line if it has no
// newline. A missing file is created.
func appendLine(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil {
		return err
	} else if fi.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, fi.Size()-1); err != nil {
			return err
		}
		if last[0] != '\n' {
			line = "\n" + line
		}
	}
	_, err = f.WriteString(line + "\n")
	return err
}

// ensureUserAccount makes sure the image has the `user` account and its home
// directory (ADR 0009). It runs as root at the first Init.
func ensureUserAccount(etcDir string) error {
	if _, err := addUserAccount(etcDir); err != nil {
		return fmt.Errorf("add the %s account: %w", DefaultUser, err)
	}
	id, err := resolveUser(DefaultUser, etcDir)
	if err != nil {
		return err
	}
	// Only a home that isn't there yet is created and handed over. An
	// existing one, or a symlink the image put there, is the image's.
	if _, err := os.Lstat(id.home); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// A missing /home must stay traversable by everyone.
	if err := os.MkdirAll(filepath.Dir(id.home), 0o755); err != nil {
		return err
	}
	if err := os.Mkdir(id.home, 0o750); err != nil {
		return err
	}
	if err := os.Lchown(id.home, int(id.cred.Uid), int(id.cred.Gid)); err != nil {
		return err
	}
	// MkdirAll's mode is masked by the umask.
	return os.Chmod(id.home, 0o750)
}
