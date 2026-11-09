// Package ghtoken reads the GitHub token every write command here uses, from a
// file and nowhere else: never a flag value, the environment or a URL.
//
// It was six copies of one function (ghissue, ghpr, ghrel, ghrepo, ghrun,
// pullresidue), and five of them read the whole file before comparing its
// size with the bound. This one asks the file's size first, so a large file
// named by mistake is refused without being read.
package ghtoken

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MaxBytes bounds the token file: a token is tens of bytes.
const MaxBytes = 4 << 10

// Read returns the token in path (default ~/.github-token). The file must be a
// regular file that only its owner may read: the mode check is the point, a
// token in a world-readable file is already spent.
func Read(path string) (string, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, ".github-token")
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("token file %s: not a regular file", path)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("token file %s: mode %04o, want owner-only (0600)", path, st.Mode().Perm())
	}
	if st.Size() > MaxBytes {
		return "", fmt.Errorf("token file %s: over %d bytes", path, MaxBytes)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	defer f.Close()
	// Bounded again: the file may have grown since Stat.
	b, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	if len(b) > MaxBytes {
		return "", fmt.Errorf("token file %s: over %d bytes", path, MaxBytes)
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", fmt.Errorf("token file %s: empty", path)
	}
	return tok, nil
}
