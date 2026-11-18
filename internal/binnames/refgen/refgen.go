// Package main regenerates internal/binnames/ref/*.txt from the sources it
// names. Run it from the repository root:
//
//	go run ./internal/binnames/refgen
//
// ⛔ The lists are PRODUCED, not typed. A reference written from memory is a
// list of the commands one remembers, and the next collision is the command one
// did not. Each file records the URL it was read from and the date.
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	posixURL    = "https://pubs.opengroup.org/onlinepubs/9799919799/idx/utilities.html"
	popconURL   = "https://popcon.debian.org/by_inst"
	contentsURL = "https://deb.debian.org/debian/dists/stable/main/Contents-amd64.gz"
	windowsURL  = "https://raw.githubusercontent.com/MicrosoftDocs/windowsserverdocs/main/WindowsServerDocs/administration/windows-commands/windows-commands.md"
)

// ParsePOSIX reads the Utilities index of POSIX.1-2024 (IEEE Std 1003.1-2024).
func ParsePOSIX(html []byte) []string {
	re := regexp.MustCompile(`<a href="\.\./utilities/[^"]+"[^>]*>([^<]+)</a>`)
	return uniq(submatches(re, html))
}

// ParseWindows reads Microsoft's Windows commands A-Z index. Entries of more
// than one word (`add alias`, `net user`) are subcommands, not commands.
func ParseWindows(md []byte) []string {
	re := regexp.MustCompile("(?m)^- \\[`([^`]+)`\\]\\(")
	var out []string
	for _, n := range submatches(re, md) {
		if !strings.ContainsAny(n, " /") {
			out = append(out, n)
		}
	}
	return uniq(out)
}

// ParsePopcon returns the package names of the top n rows of popcon's by_inst.
func ParsePopcon(r io.Reader, n int) ([]string, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var out []string
	for sc.Scan() && len(out) < n {
		f := strings.Fields(sc.Text())
		if len(f) < 3 || strings.HasPrefix(f[0], "#") {
			continue
		}
		if _, err := strconv.Atoi(f[0]); err != nil {
			continue // "Total" and separator rows
		}
		out = append(out, f[1])
	}
	return out, sc.Err()
}

// A Binary is a command a Debian package ships.
type Binary struct{ Name, Package string }

var binPath = regexp.MustCompile(`^(?:usr/)?s?bin/([^/]+)$`)
var jvmPath = regexp.MustCompile(`^usr/lib/jvm/[^/]+/bin/([^/]+)$`)

// ParseContents reads a Debian Contents file and returns, per package, the
// names it puts in a bin directory. Java puts its tools under /usr/lib/jvm and
// reaches /usr/bin through update-alternatives, which Contents does not list,
// so those paths count too.
func ParseContents(r io.Reader) (map[string][]string, error) {
	pkgs := map[string][]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		i := strings.LastIndexAny(line, " \t")
		if i < 0 {
			continue
		}
		path, owners := strings.TrimSpace(line[:i]), line[i+1:]
		var name string
		if m := binPath.FindStringSubmatch(path); m != nil {
			name = m[1]
		} else if m := jvmPath.FindStringSubmatch(path); m != nil {
			name = m[1]
		} else {
			continue
		}
		for _, o := range strings.Split(owners, ",") {
			_, pkg, ok := strings.Cut(o, "/")
			if !ok {
				pkg = o
			}
			pkgs[pkg] = append(pkgs[pkg], name)
			// ⛔ Debian ships kinit.mit and kinit.heimdal and points `kinit` at
			// one of them with update-alternatives, which Contents does not
			// list. Reading only the file names would leave the very command
			// this list exists to protect out of it.
			for _, alt := range []string{".mit", ".heimdal"} {
				if base := strings.TrimSuffix(name, alt); base != name && base != "" {
					pkgs[pkg] = append(pkgs[pkg], base)
				}
			}
		}
	}
	return pkgs, sc.Err()
}

// A Suite names a family of tools by the Debian packages that ship them.
type Suite struct {
	Label string
	Match *regexp.Regexp
}

// Suites are the well-known tool families the lists must cover whatever their
// popularity: a collision with `kinit` matters even where nobody installs krb5.
var Suites = []Suite{
	{"coreutils", regexp.MustCompile(`^coreutils$`)},
	{"util-linux", regexp.MustCompile(`^util-linux(-extra)?$`)},
	{"iproute2", regexp.MustCompile(`^iproute2$`)},
	{"OpenSSH", regexp.MustCompile(`^openssh-(client|server|sftp-server)$`)},
	{"MIT krb5", regexp.MustCompile(`^krb5-(user|kdc|admin-server|kpropd|multidev|gss-samples|pkinit)$`)},
	{"Heimdal", regexp.MustCompile(`^heimdal-(clients|kdc|servers|dev|multidev)$`)},
	{"git", regexp.MustCompile(`^git$`)},
	{"docker", regexp.MustCompile(`^(docker\.io|docker-cli|docker-compose|docker-buildx)$`)},
	{"systemd", regexp.MustCompile(`^systemd(-.*)?$`)},
	{"Java bin", regexp.MustCompile(`^openjdk-[0-9]+-(jre|jdk)(-headless)?$`)},
	{"Python", regexp.MustCompile(`^(python3|python3-minimal|python3-pip|python-is-python3|python3\.[0-9]+(-minimal)?)$`)},
	{"Node", regexp.MustCompile(`^(nodejs|npm)$`)},
}

// A Page is a document whose text must contain each name written from it.
type Page struct {
	URL   string
	Label string
	Names []string
}

// Extras are tools Debian does not package. Every name is written only if it
// is found in the page named for it: a name not on the page is an error here.
var Extras = []Page{
	{"https://kubernetes.io/docs/reference/kubectl/", "kubectl", []string{"kubectl"}},
	{"https://kubernetes.io/releases/download/", "Kubernetes", []string{"kubeadm", "kubelet"}},
	{"https://docs.docker.com/reference/cli/docker/", "docker", []string{"docker"}},
	{"https://docs.docker.com/reference/cli/dockerd/", "docker", []string{"dockerd"}},
	// MIT Kerberos for Windows ships kinit.exe too; its names are the MIT krb5
	// ones, already in suites.txt, and Normalise folds the .exe.
	{"https://docs.oracle.com/en/java/javase/21/docs/specs/man/kinit.html", "Java bin (JDK tool)", []string{"kinit"}},
	{"https://docs.oracle.com/en/java/javase/21/docs/specs/man/klist.html", "Java bin (JDK tool)", []string{"klist"}},
	{"https://docs.oracle.com/en/java/javase/21/docs/specs/man/ktab.html", "Java bin (JDK tool)", []string{"ktab"}},
}

// VerifyOnPage returns the names that occur in text as whole words.
func VerifyOnPage(text []byte, names []string) (found, missing []string) {
	for _, n := range names {
		re := regexp.MustCompile(`(?i)(^|[^A-Za-z0-9_.-])` + regexp.QuoteMeta(n) + `($|[^A-Za-z0-9_-])`)
		if re.Match(text) {
			found = append(found, n)
		} else {
			missing = append(missing, n)
		}
	}
	return found, missing
}

// Header is the provenance block that opens every list.
func Header(list string, sources []string, fetched string, note string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# list: %s\n", list)
	for _, s := range sources {
		fmt.Fprintf(&b, "# source: %s\n", s)
	}
	fmt.Fprintf(&b, "# fetched: %s\n", fetched)
	if note != "" {
		fmt.Fprintf(&b, "# %s\n", note)
	}
	return b.String()
}

func submatches(re *regexp.Regexp, b []byte) []string {
	var out []string
	for _, m := range re.FindAllSubmatch(b, -1) {
		out = append(out, strings.TrimSpace(string(m[1])))
	}
	return out
}

func uniq(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range s {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func get(url string) ([]byte, error) {
	cl := &http.Client{Timeout: 5 * time.Minute}
	resp, err := cl.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func writeList(dir, file, header string, rows []string) error {
	sort.Strings(rows)
	rows = uniq(rows)
	if len(rows) == 0 {
		return fmt.Errorf("%s: no names read: refusing to write an empty list", file)
	}
	return os.WriteFile(filepath.Join(dir, file), []byte(header+strings.Join(rows, "\n")+"\n"), 0o644)
}

func macOSNames() (rows []string, version string, err error) {
	if runtime.GOOS != "darwin" {
		return nil, "", fmt.Errorf("the macOS list is read from a macOS host; this is %s", runtime.GOOS)
	}
	v, err := exec.Command("sw_vers", "-productVersion").Output()
	if err != nil {
		return nil, "", err
	}
	for _, d := range []string{"/bin", "/sbin", "/usr/bin", "/usr/sbin"} {
		ents, err := os.ReadDir(d)
		if err != nil {
			return nil, "", err
		}
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			rows = append(rows, e.Name()+"\t"+d)
		}
	}
	return rows, strings.TrimSpace(string(v)), nil
}

func run(outDir, fetched string, topN int) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	fail := false
	report := func(what string, err error) {
		fmt.Fprintf(os.Stderr, "refgen: %s: %v\n", what, err)
		fail = true
	}

	if b, err := get(posixURL); err != nil {
		report("POSIX", err)
	} else {
		var rows []string
		for _, n := range ParsePOSIX(b) {
			rows = append(rows, n+"\tPOSIX.1-2024 utility")
		}
		report0(report, "POSIX", writeList(outDir, "posix.txt",
			Header("POSIX utilities", []string{posixURL}, fetched, "IEEE Std 1003.1-2024 (The Open Group Base Specifications Issue 8), Shell & Utilities, Utilities index"), rows))
	}

	if b, err := get(windowsURL); err != nil {
		report("Windows", err)
	} else {
		var rows []string
		for _, n := range ParseWindows(b) {
			rows = append(rows, n+"\tWindows command reference")
		}
		report0(report, "Windows", writeList(outDir, "windows.txt",
			Header("Windows commands", []string{windowsURL}, fetched,
				"Microsoft's A-Z index of Windows commands; includes cmd.exe built-ins and diskpart/netsh sub-commands (clean, add, list), which are not files on disk but are names a Windows user already types"), rows))
	}

	if rows, ver, err := macOSNames(); err != nil {
		report("macOS", err)
	} else {
		report0(report, "macOS", writeList(outDir, "macos.txt",
			Header("macOS system commands", []string{"this host, macOS " + ver + ": ls /bin /sbin /usr/bin /usr/sbin"}, fetched, ""), rows))
	}

	pop, err1 := get(popconURL)
	cont, err2 := get(contentsURL)
	switch {
	case err1 != nil:
		report("popcon", err1)
	case err2 != nil:
		report("Contents", err2)
	default:
		zr, err := gzip.NewReader(bytes.NewReader(cont))
		if err != nil {
			report("Contents", err)
			break
		}
		pkgs, err := ParseContents(zr)
		if err != nil {
			report("Contents", err)
			break
		}
		top, err := ParsePopcon(bytes.NewReader(pop), topN)
		if err != nil {
			report("popcon", err)
			break
		}
		var popRows, suiteRows []string
		for rank, p := range top {
			for _, n := range pkgs[p] {
				popRows = append(popRows, fmt.Sprintf("%s\t%s (popcon rank %d)", n, p, rank+1))
			}
		}
		for p, names := range pkgs {
			for _, s := range Suites {
				if s.Match.MatchString(p) {
					for _, n := range names {
						suiteRows = append(suiteRows, fmt.Sprintf("%s\t%s (package %s)", n, s.Label, p))
					}
				}
			}
		}
		report0(report, "Debian popularity", writeList(outDir, "debian-popcon.txt",
			Header("Debian most installed", []string{popconURL, contentsURL}, fetched,
				fmt.Sprintf("names in /bin /sbin /usr/bin /usr/sbin of the %d most installed packages (popularity-contest by_inst), stable amd64", topN)), popRows))
		report0(report, "Debian suites", writeList(outDir, "suites.txt",
			Header("Well-known tool suites", []string{contentsURL}, fetched,
				"the commands of the packages that ship coreutils, util-linux, iproute2, OpenSSH, MIT krb5, Heimdal, git, docker, systemd, Java, Python and Node, whatever their popularity"), suiteRows))
	}

	var extra []string
	var srcs []string
	for _, p := range Extras {
		b, err := get(p.URL)
		if err != nil {
			report("extra "+p.URL, err)
			continue
		}
		found, missing := VerifyOnPage(b, p.Names)
		if len(missing) > 0 {
			report("extra "+p.URL, fmt.Errorf("%v not on the page", missing))
		}
		for _, n := range found {
			extra = append(extra, n+"\t"+p.Label+"\t"+p.URL)
		}
		if len(found) > 0 {
			srcs = append(srcs, p.URL)
		}
	}
	report0(report, "extras", writeList(outDir, "extras.txt",
		Header("Tools Debian does not package", srcs, fetched, "each name was found in the page cited"), extra))

	if fail {
		return fmt.Errorf("some lists were not regenerated; the ones written are current, the others are as they were")
	}
	return nil
}

func report0(report func(string, error), what string, err error) {
	if err != nil {
		report(what, err)
	}
}

func main() {
	out := "internal/binnames/ref"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	if err := run(out, time.Now().Format("2006-01-02"), 2000); err != nil {
		fmt.Fprintln(os.Stderr, "refgen:", err)
		os.Exit(1)
	}
}
