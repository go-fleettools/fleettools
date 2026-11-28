package main

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func TestUnattendXMLIsWellFormed(t *testing.T) {
	doc := unattendXML("Windows 11 Pro", "pw")
	dec := xml.NewDecoder(strings.NewReader(doc))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("answer file is not well-formed XML: %v", err)
		}
	}
}

func TestUnattendXMLContent(t *testing.T) {
	doc := unattendXML("Windows 11 Pro", "s3cret")
	for _, k := range labConfigBypass {
		if !strings.Contains(doc, "/v "+k+" ") {
			t.Errorf("missing LabConfig bypass %s", k)
		}
	}
	for _, want := range []string{genericProKeyValue, "<Value>Windows 11 Pro</Value>", provisionName, "<Value>s3cret</Value>"} {
		if !strings.Contains(doc, want) {
			t.Errorf("answer file lacks %q", want)
		}
	}
}

func TestUnattendXMLEscapesEdition(t *testing.T) {
	doc := unattendXML(`A<&"B`, "pw")
	if strings.Contains(doc, `A<&"B`) {
		t.Fatal("edition is not escaped")
	}
	if !strings.Contains(doc, "A&lt;&amp;") {
		t.Fatalf("escaped edition not found")
	}
	if strings.Contains(doc, genericProKeyValue) {
		t.Fatal("the generic Pro key must only go with the Pro edition")
	}
	dec := xml.NewDecoder(strings.NewReader(doc))
	for {
		if _, err := dec.Token(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("not well-formed: %v", err)
		}
	}
}

func TestRandomPassword(t *testing.T) {
	a, err := randomPassword(24)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := randomPassword(24)
	if len(a) != 24 || a == b {
		t.Fatalf("passwords %q %q", a, b)
	}
	if strings.ContainsAny(a, `<>&"'$`+"`") {
		t.Fatalf("password %q needs quoting", a)
	}
}

func TestProvisionScriptPrintsTheMarker(t *testing.T) {
	if !strings.Contains(provisionScript, buildMarker) {
		t.Fatalf("winprovision.ps1 never prints %q", buildMarker)
	}
}

func TestUnattendCreatesTheBuildAccount(t *testing.T) {
	doc := unattendXML("Windows 11 Pro", "pw")
	for _, want := range []string{"<LocalAccounts>", "<Name>" + buildUser + "</Name>", "<Group>Administrators</Group>", "<Username>" + buildUser + "</Username>"} {
		if !strings.Contains(doc, want) {
			t.Errorf("answer file lacks %q", want)
		}
	}
	if !strings.Contains(provisionScript, "net user "+buildUser+" /active:no") {
		t.Error("the provisioning script leaves the build account active")
	}
}

func TestProvisionScriptFollowsCloudbase(t *testing.T) {
	for _, want := range []string{"SetSetupComplete.cmd", "0e7fa42e0cbc0ce7657f85730b0c6cc7afc4087a3639df0ff51a721a0be19bd5", "CloudbaseInitSetup_1_1_8_x64.msi"} {
		if !strings.Contains(provisionScript, want) {
			t.Errorf("winprovision.ps1 lacks %q", want)
		}
	}
	if strings.Contains(provisionScript, "_Stable_") {
		t.Error("the cloudbase-init MSI must be pinned, not the moving stable URL")
	}
	if i, j := strings.Index(provisionScript, "SetSetupComplete.cmd"), strings.Index(provisionScript, "sysprep.exe"); i < 0 || j < 0 || i > j {
		t.Error("SetSetupComplete.cmd must run before sysprep")
	}
}

func TestBuilderBlockDevices(t *testing.T) {
	bd := builderBlockDevices("root", "boot", "iso", "ans")
	if len(bd) != 4 {
		t.Fatalf("got %d devices", len(bd))
	}
	if bd[0].UUID != "root" || bd[0].BootIndex != 0 {
		t.Fatalf("root must be first with boot index 0: %+v", bd[0])
	}
	for _, d := range bd {
		if d.SourceType != "volume" {
			t.Errorf("%s: source %q, want a ready volume", d.UUID, d.SourceType)
		}
		if !d.DeleteOnTermination {
			t.Errorf("%s survives the builder", d.UUID)
		}
	}
}

func TestValidate(t *testing.T) {
	ok := winOpts{ISO: "x.iso", Edition: "e", Network: "n", Group: "g", Timeout: 1}
	if err := ok.validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*winOpts){
		"no iso":     func(o *winOpts) { o.ISO = "" },
		"no edition": func(o *winOpts) { o.Edition = "" },
		"no network": func(o *winOpts) { o.Network = "" },
		"no group":   func(o *winOpts) { o.Group = "" },
		"no timeout": func(o *winOpts) { o.Timeout = 0 },
	} {
		o := ok
		mut(&o)
		if o.validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	up := winOpts{ISO: "x.iso", Edition: "e", Timeout: 1, UploadOnly: true}
	if err := up.validate(); err != nil {
		t.Errorf("upload-only needs no network: %v", err)
	}
}

// The first-logon command must show a console and write to COM1 itself, and the
// specialize pass must announce itself on COM1, so a silent guest is told apart
// from one whose command never ran.
func TestUnattendXMLReportsOnCOM1(t *testing.T) {
	doc := unattendXML("Windows 11 Pro", "pw")
	for _, want := range []string{"-WindowStyle Normal", `\\.\COM1`, "firstlogon command started", "OSVM: specialize pass", "<Description>", "is on no drive"} {
		if !strings.Contains(doc, want) {
			t.Errorf("answer file lacks %q", want)
		}
	}
	if !strings.Contains(provisionScript, `cmd.exe /c "echo OSVM: $c>\\.\COM1"`) {
		t.Error("the provisioning script does not write COM1 through cmd echo")
	}
	// no PowerShell write to COM1 anywhere: it was never shown to work
	for name, s := range map[string]string{"answer file": doc, "script": provisionScript} {
		if strings.Contains(s, "OpenWrite") || strings.Contains(s, "SerialPort 'COM1'") {
			t.Errorf("%s writes COM1 from PowerShell", name)
		}
	}
}

// The three markers, in the order a healthy guest shows them: specialize,
// SetupComplete (OOBE finished), first-logon pass (before PowerShell starts).
func TestUnattendXMLMarkers(t *testing.T) {
	doc := unattendXML("Windows 11 Pro", "pw")
	order := []string{"OSVM: specialize pass", "SetupComplete.cmd echo echo OSVM: setupcomplete^&gt;\\\\.\\COM1", "cmd.exe /c echo OSVM: firstlogon pass&gt;\\\\.\\COM1", "osvm provisioning"}
	last := -1
	for _, w := range order {
		i := strings.Index(doc, w)
		if i < 0 {
			t.Fatalf("answer file lacks %q", w)
		}
		if i < last {
			t.Errorf("%q is out of order", w)
		}
		last = i
	}
	if strings.Contains(doc, "pass>\\") {
		t.Error("an unescaped > in the answer file")
	}
}
