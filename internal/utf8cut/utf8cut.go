// Package utf8cut cuts byte streams on UTF-8 character boundaries, so a
// piece of output never starts or ends inside a multi-byte character.
package utf8cut

import "unicode/utf8"

// SplitIncomplete splits b before an incomplete character at its end.
func SplitIncomplete(b []byte) (whole, rest []byte) {
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

// TrimPartialStart drops the continuation bytes of a character that began
// before b, at most three.
func TrimPartialStart(b []byte) []byte {
	for i := 0; i < len(b) && i < utf8.UTFMax-1; i++ {
		if utf8.RuneStart(b[i]) {
			return b[i:]
		}
	}
	if len(b) >= utf8.UTFMax-1 {
		return b[utf8.UTFMax-1:]
	}
	return b[len(b):]
}
