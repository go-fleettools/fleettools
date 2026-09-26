package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// UnlistedFile is read from the root of a checked-out tree.
const UnlistedFile = ".docs-unlisted"

// unlisted is org -> repository -> the reason it is not advertised.
//
// ⛔ THE CHECK TOLD PEOPLE TO DO SOMETHING IT DID NOT LET THEM DO. Its failure
// message reads: "Being unlisted can be a choice; if it is, say so by listing
// it and marking it, not by leaving it invisible." There was no marking. The
// only way to make the check pass was to put a card on the page — including
// for go-composites/is, whose whole content is a README with a banner and a
// heading. A row pointing at nothing is worse than no row, so the instruction
// as given made the page worse.
//
// The file lives in the tree under review, so the allowance turns up in the
// pull request's own diff rather than in a setting somewhere. It is read from
// the tree for the same reason everything else is: a branch is judged on what
// it proposes.
var unlisted = map[string]map[string]string{}

// A REASON IS REQUIRED. An escape hatch that can be used silently is a way to
// switch the check off one line at a time; one that has to say why is a note to
// the next reader.
func readUnlisted(org, root string) error {
	b, err := os.ReadFile(filepath.Join(root, UnlistedFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, reason, _ := strings.Cut(line, " ")
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return fmt.Errorf("%s:%d: %q says which repository but not why; write the reason after the name", UnlistedFile, i+1, name)
		}
		if unlisted[org] == nil {
			unlisted[org] = map[string]string{}
		}
		unlisted[org][name] = reason
	}
	return nil
}

// orgOf is the owner half of owner/repo.
func orgOf(repo string) string {
	org, _, _ := strings.Cut(repo, "/")
	return org
}
