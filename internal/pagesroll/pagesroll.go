// Package pagesroll inserts the pagesbuild step into a docs repository's
// .github/workflows/docs.yml, exactly as go-redfish/docs#11 did, and refuses a
// workflow whose deploy job does not have the shape that change was proven on.
package pagesroll

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Workflow is the path of the file, relative to a docs checkout.
const Workflow = ".github/workflows/docs.yml"

// DefaultVersion is the fleettools release the reference change pinned.
const DefaultVersion = "v0.80.0"

// ErrRefused wraps every refusal: the workflow is not the known shape.
var ErrRefused = errors.New("refused")

// ErrAlready: the workflow already runs pagesbuild.
var ErrAlready = errors.New("already has pagesbuild")

var versionRE = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

const stepTemplate = `
      # ⛔ A GREEN PUSH IS NOT A PUBLISHED SITE. GitHub's legacy "pages build and
      # deployment" for the gh-pages branch sometimes stalls in updating_pages and
      # fails with "Timeout reached, aborting!" after 10 minutes (3 times on
      # 2026-11-05/06, runs 42058763350 and 42252993462); re-running it does not
      # help and re-running this workflow pushes nothing. pagesbuild waits for a
      # build of the pushed commit to be "built", requests a new one when it is
      # not, and fails this job if the site is still not built.
      - name: Wait for the Pages build
        if: steps.push.outputs.sha != ''
        env:
          GH_TOKEN: ${{ github.token }}
        run: |
          umask 077
          printf '%s' "${GH_TOKEN}" > "${RUNNER_TEMP}/pages-token"
          go run github.com/go-fleettools/fleettools/cmd/pagesbuild@VERSION \
            --token-file "${RUNNER_TEMP}/pages-token" \
            "${GITHUB_REPOSITORY}" "${{ steps.push.outputs.sha }}"
`

const echoLine = `          echo "sha=$(git rev-parse HEAD)" >> "$GITHUB_OUTPUT"`
const pushLine = `          git push origin HEAD:gh-pages`

func refuse(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrRefused, fmt.Sprintf(format, a...))
}

func indent(l string) int { return len(l) - len(strings.TrimLeft(l, " ")) }

func isJobKey(l string) bool {
	return indent(l) == 2 && strings.HasSuffix(l, ":") && !strings.HasPrefix(strings.TrimSpace(l), "#")
}

// Apply returns src with the change made. It returns ErrAlready (and src)
// when pagesbuild is there, and an ErrRefused when the deploy job is not the
// known shape; it never returns a half-edited text.
func Apply(src, version string) (string, error) {
	if !versionRE.MatchString(version) {
		return "", fmt.Errorf("version %q is not vMAJOR.MINOR.PATCH", version)
	}
	if strings.Contains(src, "cmd/pagesbuild@") {
		return src, ErrAlready
	}
	if strings.Contains(src, "\r") {
		return "", refuse("CRLF line endings")
	}
	lines := strings.Split(src, "\n")

	start := -1
	for i, l := range lines {
		if l == "  deploy:" {
			if start >= 0 {
				return "", refuse("two deploy jobs")
			}
			start = i
		}
	}
	if start < 0 {
		return "", refuse("no deploy job")
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if isJobKey(lines[i]) {
			end = i
			break
		}
	}

	// permissions: contents: write, and nothing else, then steps.
	perm := -1
	for i := start + 1; i < end; i++ {
		if lines[i] == "    permissions:" {
			if perm >= 0 {
				return "", refuse("two permissions blocks in the deploy job")
			}
			perm = i
		}
	}
	if perm < 0 || perm+2 >= end || lines[perm+1] != "      contents: write" || lines[perm+2] != "    steps:" {
		return "", refuse("deploy job permissions are not exactly 'contents: write' followed by steps")
	}

	setupGo := false
	push := -1
	for i := perm + 3; i < end; i++ {
		if strings.Contains(lines[i], "uses: actions/setup-go@") {
			setupGo = true
		}
		if lines[i] == "      - name: Push gh-pages" {
			if push >= 0 {
				return "", refuse("two 'Push gh-pages' steps")
			}
			push = i
		}
	}
	if !setupGo {
		return "", refuse("deploy job has no actions/setup-go step, which the new step's go run needs")
	}
	if push < 0 || push+2 >= end ||
		lines[push+1] != "        working-directory: gh-pages" || lines[push+2] != "        run: |" {
		return "", refuse("no 'Push gh-pages' step with working-directory gh-pages and a literal run block")
	}
	// The step ends at the next line indented 6 or less, or at the job's end.
	stepEnd := end
	for i := push + 1; i < end; i++ {
		if strings.TrimSpace(lines[i]) != "" && indent(lines[i]) <= 6 {
			stepEnd = i
			break
		}
	}
	last := stepEnd - 1
	for last > push && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	if lines[last] != pushLine {
		return "", refuse("the 'Push gh-pages' step does not end with %q", strings.TrimSpace(pushLine))
	}
	for i := push + 3; i <= last; i++ {
		if strings.Contains(lines[i], "GITHUB_OUTPUT") {
			return "", refuse("the 'Push gh-pages' step already writes GITHUB_OUTPUT")
		}
	}

	step := strings.Split(strings.TrimSuffix(strings.ReplaceAll(stepTemplate, "VERSION", version), "\n"), "\n")
	var out []string
	out = append(out, lines[:perm+2]...)
	out = append(out, "      pages: write")
	out = append(out, lines[perm+2:push+1]...)
	out = append(out, "        id: push")
	out = append(out, lines[push+1:last+1]...)
	out = append(out, echoLine)
	out = append(out, step...)
	out = append(out, lines[last+1:]...)
	return strings.Join(out, "\n"), nil
}

// Lint runs actionlint on file and returns its report ("" when clean). An
// empty binary skips the check and says so by returning ("", nil).
func Lint(actionlint, file string) (string, error) {
	if actionlint == "" {
		return "", nil
	}
	cmd := exec.Command(actionlint, file)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return "", nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), nil
	}
	return "", fmt.Errorf("actionlint: %w", err)
}

// Edit applies the change to dir's docs.yml and lints the result. The file is
// restored to what it was if the edit introduces an actionlint finding the
// original did not have.
func Edit(dir, version, actionlint string) (string, error) {
	p := filepath.Join(dir, Workflow)
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	src := string(b)
	out, err := Apply(src, version)
	if err != nil {
		return "", err
	}
	before, err := Lint(actionlint, p)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
		return "", err
	}
	after, err := Lint(actionlint, p)
	if err != nil {
		_ = os.WriteFile(p, b, 0o644)
		return "", err
	}
	if after != "" && before == "" {
		_ = os.WriteFile(p, b, 0o644)
		return "", refuse("actionlint reports on the edited workflow:\n%s", after)
	}
	if after != "" {
		return "edited " + Workflow + " (actionlint already reported on the original; unchanged check, report kept)", nil
	}
	return "edited " + Workflow + " (actionlint clean)", nil
}
