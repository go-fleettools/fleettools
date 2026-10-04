package main

import (
	"os"
	"strings"
	"testing"
)

// Each fixture is one STEXT frame cut from a real `-gcflags=-S` dump for
// GOARCH=loong64, with the absolute source paths shortened. Together they are
// the three cases this command has to tell apart -- and the last two are
// mistakes it actually made.
func scanFile(t *testing.T, name string) Report {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rep, err := Scan(f)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// The frame golang/go#81000 quotes: go1.27.1 spills a uint8 result onto two
// CONSECUTIVE bytes, -276 and -275. Those two offsets are in the upstream
// report's own assembly listing, which is what makes this a control and not
// just an example.
func TestAHazardIsASpillWithALiveNeighbour(t *testing.T) {
	rep := scanFile(t, "hazard-stride1.s")

	if rep.Hazards() != 2 {
		t.Errorf("hazards = %d, want 2", rep.Hazards())
	}
	if len(rep.Frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(rep.Frames))
	}
	f := rep.Frames[0]
	if !f.Hazard {
		t.Error("frame is not reported hazardous")
	}
	if f.Stride != 1 {
		t.Errorf("stride = %d, want 1", f.Stride)
	}
	if !strings.HasSuffix(f.Func, "TestSkimageLabToRGBOutOfGamut") {
		t.Errorf("func = %q", f.Func)
	}
	want := []int{-276, -275}
	if len(f.Spills) != len(want) {
		t.Fatalf("spills = %d, want %d", len(f.Spills), len(want))
	}
	for i, off := range want {
		if f.Spills[i].Off != off {
			t.Errorf("spill %d at %d, want %d", i, f.Spills[i].Off, off)
		}
	}
	if got := f.Spills[0].TruncAt; got != "skimage.go:144" {
		t.Errorf("conversion at %q, want skimage.go:144", got)
	}
	// This fixture comes from go1.27.1, which does not have the fix.
	if rep.Fpgp != 0 {
		t.Errorf("MOV[WV]fpgp = %d, want 0", rep.Fpgp)
	}
}

// The same package's other spilling frame, under the SAME toolchain: two
// stores onto -96 and -88. Eight bytes apart and both 8-aligned, so each one
// fills its own slot. Reporting this would make the command useless, because
// go1.26.4 -- which passes on loong64 -- spills here too.
func TestASpillThatFillsItsOwnSlotIsNotAHazard(t *testing.T) {
	rep := scanFile(t, "safe-stride8.s")

	if rep.Hazards() != 0 {
		t.Errorf("hazards = %d, want 0", rep.Hazards())
	}
	if rep.Spilled() != 2 {
		t.Errorf("spilled = %d, want 2", rep.Spilled())
	}
	if len(rep.Frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(rep.Frames))
	}
	if f := rep.Frames[0]; f.Stride != 8 {
		t.Errorf("stride = %d, want 8", f.Stride)
	}
}

// A dump holds a package more than once -- for itself and again as a test
// binary's dependency -- so one instruction is listed twice. Counting both
// gave `int(dpi)` in go-pdfkit/conformance a stride of 0 between a spill and
// ITSELF, and a stride-under-8 rule then called an 8-aligned int slot
// hazardous. The duplicate must collapse, and a zero gap must not count.
func TestTheSameInstructionTwiceIsOneSpillAndNoStride(t *testing.T) {
	rep := scanFile(t, "safe-duplicated-frame.s")

	if rep.Spilled() != 1 {
		t.Errorf("spilled = %d, want 1 (the duplicate must collapse)", rep.Spilled())
	}
	if rep.Hazards() != 0 {
		t.Errorf("hazards = %d, want 0", rep.Hazards())
	}
	if len(rep.Frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(rep.Frames))
	}
	if f := rep.Frames[0]; f.Stride != 0 {
		t.Errorf("stride = %d, want 0 (one spill has no stride)", f.Stride)
	}
}

// The regexp for a stack slot has to accept the characters a fully qualified
// symbol actually contains. A class that left out `/` matched nothing, so the
// first version of this command reported 0 hazards on the fixture above --
// a scan that cannot read reports the same thing as a clean build.
func TestAQualifiedSlotNameIsMatched(t *testing.T) {
	const line = "\t0x01e4 00484 (/src/color/skimage_test.go:118)\tMOVD\tF6, github.com/go-gfx/gfx/color.~r0-275(SP)"
	in := "github.com/go-gfx/gfx/color.T STEXT size=1\n" +
		"\t0x0000 00000 (/src/color/skimage.go:144)\tTRUNCDW\tF6, F6\n" + line + "\n"

	rep, err := Scan(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Spilled() != 1 {
		t.Fatalf("spilled = %d, want 1 -- the slot name was not matched", rep.Spilled())
	}
	s := rep.Frames[0].Spills[0]
	if s.Slot != "github.com/go-gfx/gfx/color.~r0" {
		t.Errorf("slot = %q", s.Slot)
	}
	if s.Off != -275 {
		t.Errorf("off = %d, want -275", s.Off)
	}
}

// A result that goes straight to a general register is settled: it is no
// longer a float, so nothing can spill it as one. Without this the command
// would blame the next unrelated MOVD in the frame.
func TestAResultDrainedToAGPRIsNotPendingAnyMore(t *testing.T) {
	in := "pkg.F STEXT size=1\n" +
		"\t0x0000 00000 (/src/a.go:1)\tTRUNCDW\tF6, F6\n" +
		"\t0x0004 00004 (/src/a.go:1)\tMOVV\tF6, R4\n" +
		"\t0x0008 00008 (/src/a.go:2)\tMOVD\tF6, pkg.x-9(SP)\n"

	rep, err := Scan(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Drained != 1 {
		t.Errorf("drained = %d, want 1", rep.Drained)
	}
	if rep.Spilled() != 0 {
		t.Errorf("spilled = %d, want 0", rep.Spilled())
	}
}

// A toolchain carrying CL 820040 wraps the conversions, and the report has to
// say so rather than leaving the reader to infer which Go this was.
func TestTheFixIsReportedWhenPresent(t *testing.T) {
	in := "pkg.F STEXT size=1\n" +
		"\t0x0000 00000 (/src/a.go:1)\tTRUNCDW\tF6, F6\n" +
		"\t0x0004 00004 (/src/a.go:1)\tMOVWfpgp\tF6, R4\n"

	rep, err := Scan(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Fpgp != 1 {
		t.Errorf("MOV[WV]fpgp = %d, want 1", rep.Fpgp)
	}
}

// An offset that is not a multiple of 8 cannot be the base of an 8-byte slot,
// so a single store is already enough to run past it -- there is no second
// spill to form a stride with.
func TestAMisalignedOffsetIsAHazardOnItsOwn(t *testing.T) {
	in := "pkg.F STEXT size=1\n" +
		"\t0x0000 00000 (/src/a.go:1)\tTRUNCDW\tF6, F6\n" +
		"\t0x0004 00004 (/src/a.go:2)\tMOVD\tF6, pkg.x-9(SP)\n"

	rep, err := Scan(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Hazards() != 1 {
		t.Errorf("hazards = %d, want 1", rep.Hazards())
	}
	if f := rep.Frames[0]; f.Stride != 0 {
		t.Errorf("stride = %d, want 0", f.Stride)
	}
}
