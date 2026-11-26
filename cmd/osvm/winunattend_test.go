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
