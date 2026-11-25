package guestagent

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// identity is who a process the Guest Agent starts runs as.
type identity struct {
	cred *syscall.Credential
	home string
}

// resolveUser turns an image's USER ("name", "uid", "name:group" or
// "uid:gid") into a uid, gid and supplementary groups, reading passwd and
// group from etcDir the way Docker does from the image's own /etc:
//
//   - a name must be in passwd; a uid need not be (it then gets gid 0, home /);
//   - an explicit group, by name or gid, replaces the user's primary group;
//   - supplementary groups are the groups that list the user by name.
//
// An empty spec means root, which needs no switch (nil credential).
func resolveUser(spec, etcDir string) (identity, error) {
	if spec == "" {
		return identity{}, nil
	}
	userPart, groupPart, hasGroup := strings.Cut(spec, ":")

	passwd, err := readEtc(filepath.Join(etcDir, "passwd"))
	if err != nil {
		return identity{}, err
	}
	var name, home string
	uid, gid := -1, 0
	home = "/"
	for _, f := range passwd {
		if len(f) < 6 {
			continue
		}
		if f[0] == userPart || f[2] == userPart {
			name, home = f[0], f[5]
			uid, _ = strconv.Atoi(f[2])
			gid, _ = strconv.Atoi(f[3])
			break
		}
	}
	if uid < 0 {
		n, err := strconv.Atoi(userPart)
		if err != nil || n < 0 {
			return identity{}, fmt.Errorf("user %q is not in the image's passwd", userPart)
		}
		uid = n
	}

	groups, err := readEtc(filepath.Join(etcDir, "group"))
	if err != nil {
		return identity{}, err
	}
	if hasGroup {
		gid = -1
		for _, f := range groups {
			if len(f) >= 3 && (f[0] == groupPart || f[2] == groupPart) {
				gid, _ = strconv.Atoi(f[2])
				break
			}
		}
		if gid < 0 {
			n, err := strconv.Atoi(groupPart)
			if err != nil || n < 0 {
				return identity{}, fmt.Errorf("group %q is not in the image's group file", groupPart)
			}
			gid = n
		}
	}

	supplementary := []uint32{uint32(gid)}
	if name != "" {
		for _, f := range groups {
			if len(f) < 4 {
				continue
			}
			for _, member := range strings.Split(f[3], ",") {
				if member == name {
					if g, err := strconv.Atoi(f[2]); err == nil && uint32(g) != uint32(gid) {
						supplementary = append(supplementary, uint32(g))
					}
				}
			}
		}
	}
	return identity{
		cred: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: supplementary},
		home: home,
	}, nil
}

// readEtc reads a colon-separated file such as /etc/passwd. A missing file
// reads as empty, as it does for Docker (scratch-based images have none).
func readEtc(path string) ([][]string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rows [][]string
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rows = append(rows, strings.Split(line, ":"))
	}
	return rows, s.Err()
}
