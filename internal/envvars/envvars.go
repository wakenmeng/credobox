// Package envvars checks environment variables that come in through the
// public API (Sandbox defaults and an Exec's own).
package envvars

import (
	"fmt"
	"strings"
)

// Validate rejects names that can't be an environment variable: empty,
// containing '=' or NUL. Values may hold anything but NUL.
func Validate(env map[string]string) error {
	for k, v := range env {
		if k == "" || strings.ContainsAny(k, "=\x00") {
			return fmt.Errorf("environment variable name %q is not valid", k)
		}
		if strings.ContainsRune(v, 0) {
			return fmt.Errorf("environment variable %s contains a NUL byte", k)
		}
	}
	return nil
}
