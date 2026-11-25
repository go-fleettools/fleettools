package secretscan

import (
	"regexp"
	"strings"
)

// userinfo matches `scheme://user:password@`. The password runs to the first
// `@`, `/` or space, so `sftp://u:p@h:22/x` yields p. The user may be empty
// (`redis://:pw@host`).
var userinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*)://([^/\s:@"'?#]*):([^/\s@"']+)@`)

// URLSecret finds a password written into a URL's userinfo and returns the
// scheme and the password. A password that is an interpolation (`$PW`, `%s`,
// `{{.Pw}}`, `<pw>`) or a reference is not a value written out and is skipped.
func URLSecret(s string) (scheme, password string, ok bool) {
	for _, m := range userinfo.FindAllStringSubmatch(s, -1) {
		pw := m[3]
		if usable(pw) && !strings.HasPrefix(pw, "{") && !strings.Contains(pw, "%s") && !strings.Contains(pw, "%v") {
			return m[1], pw, true
		}
	}
	return "", "", false
}

// URLName is the name a URL-userinfo finding carries: the shape, never the
// password itself.
func URLName(scheme string) string { return scheme + "://user:***@" }

// envLike says whether s is spelled like an environment variable name:
// upper-case words joined by underscores.
func envLike(s string) bool {
	if len(s) < 5 || !strings.Contains(s, "_") || s[0] < 'A' || s[0] > 'Z' {
		return false
	}
	for _, r := range s {
		if !(r == '_' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}
