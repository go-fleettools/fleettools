package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestParsePOSIX(t *testing.T) {
	html := `<li><a href="../utilities/admin.html" target="main">admin</a>
</li>
<li><a href="../utilities/awk.html" target="main">awk</a></li><li><a href="../utilities/awk.html" target="main">awk</a></li>`
	if got, want := ParsePOSIX([]byte(html)), []string{"admin", "awk"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestParseWindowsKeepsCommandsAndDropsSubcommands(t *testing.T) {
	md := "- [`arp`](arp.md)\n- [`add alias`](add-alias.md)\n- [`net`](net.md)\nprose [`cscript`](cscript.md)\n"
	if got, want := ParseWindows([]byte(md)), []string{"arp", "net"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestParsePopcon(t *testing.T) {
	in := "#rank name inst\n1     debconf   298232 1 2 3 4 (X)\n2     libbz2-1.0 298232 1 2 3 4\n-----\n3 dpkg 1 1 1 1 1\nTotal 9 1\n"
	got, err := ParsePopcon(strings.NewReader(in), 2)
	if err != nil || !reflect.DeepEqual(got, []string{"debconf", "libbz2-1.0"}) {
		t.Fatalf("%v %v", got, err)
	}
}

func TestParseContents(t *testing.T) {
	in := "usr/bin/kinit.mit                       net/krb5-user\n" +
		"usr/bin/a file with spaces            misc/odd\n" +
		"usr/share/doc/x                         misc/odd\n" +
		"bin/ls                                  utils/coreutils,utils/busybox\n" +
		"usr/lib/jvm/java-21-openjdk-amd64/bin/keytool  java/openjdk-21-jre-headless\n" +
		"usr/bin/sub/dir                         misc/odd\n"
	pk, err := ParseContents(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	// The alternatives-managed name is the one a user types.
	if got := pk["krb5-user"]; !reflect.DeepEqual(got, []string{"kinit.mit", "kinit"}) {
		t.Errorf("krb5-user: %v", got)
	}
	if got := pk["coreutils"]; !reflect.DeepEqual(got, []string{"ls"}) {
		t.Errorf("coreutils: %v", got)
	}
	if got := pk["busybox"]; !reflect.DeepEqual(got, []string{"ls"}) {
		t.Errorf("a file with two owning packages belongs to both: %v", got)
	}
	if got := pk["openjdk-21-jre-headless"]; !reflect.DeepEqual(got, []string{"keytool"}) {
		t.Errorf("java: %v", got)
	}
	if got := pk["odd"]; !reflect.DeepEqual(got, []string{"a file with spaces"}) {
		t.Errorf("odd: %v (a nested path is not a command)", got)
	}
}

func TestSuitesMatchTheirPackages(t *testing.T) {
	want := map[string]string{
		"krb5-user": "MIT krb5", "heimdal-clients": "Heimdal", "openjdk-21-jre-headless": "Java bin",
		"python3.13-minimal": "Python", "iproute2": "iproute2", "systemd-timesyncd": "systemd", "nodejs": "Node",
	}
	for pkg, label := range want {
		ok := false
		for _, s := range Suites {
			if s.Label == label && s.Match.MatchString(pkg) {
				ok = true
			}
		}
		if !ok {
			t.Errorf("%s is not matched by suite %s", pkg, label)
		}
	}
}

// Only a name found on the cited page is written down.
func TestVerifyOnPage(t *testing.T) {
	found, missing := VerifyOnPage([]byte("run `kubectl get pods`, then kubectl-convert; not kubeadmin"), []string{"kubectl", "kubeadm", "kubectl-convert"})
	if !reflect.DeepEqual(found, []string{"kubectl", "kubectl-convert"}) || !reflect.DeepEqual(missing, []string{"kubeadm"}) {
		t.Fatalf("found %v missing %v", found, missing)
	}
}

func TestHeaderCitesItsSource(t *testing.T) {
	h := Header("L", []string{"https://x/y"}, "2026-11-18", "note")
	for _, w := range []string{"# list: L", "# source: https://x/y", "# fetched: 2026-11-18", "# note"} {
		if !strings.Contains(h, w) {
			t.Errorf("header lacks %q: %s", w, h)
		}
	}
}

func TestWriteListRefusesNothing(t *testing.T) {
	if err := writeList(t.TempDir(), "x.txt", "h", nil); err == nil {
		t.Fatal("an empty list must be refused, not written")
	}
}
