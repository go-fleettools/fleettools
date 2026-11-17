package main

import "testing"

// ⛔⛔ THE GATE MUST FIRE ON A BENCHMARK AND ON NOTHING ELSE.
//
// Too narrow and the measurement this exists for goes through ungated — which is
// how a share got published from two figures taken on a loaded machine, the
// wrong one ruling out the candidate that was right.
//
// Too wide and an ordinary test run starts being refused on a busy machine. That
// is worse than no gate: a PASS/FAIL does not move with load, so refusing it
// teaches everyone to stop using this wrapper, and the wrapper is also what
// catches a -run that matched nothing.
//
// ⚠ `-benchtime` and `-benchmem` both BEGIN with `-bench` and neither asks for a
// benchmark. A prefix match would gate `go test -benchtime 20x ./...`, which
// measures nothing at all.
func TestTheBenchGateFiresOnABenchmarkAndNothingElse(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		want bool
	}{
		{"a plain test run", []string{"./..."}, false},
		{"a filtered test run", []string{"-run", "TestX", "."}, false},

		{"-bench with a separate pattern", []string{"-bench", ".", "."}, true},
		{"-bench=", []string{"-bench=.", "."}, true},
		{"-test.bench=", []string{"-test.bench=BenchmarkX", "."}, true},
		{"--bench", []string{"--bench", "Benchmark.*", "."}, true},

		// ⛔ The too-wide rows. Each of these starts with the four letters the
		// gate looks for and asks for no benchmark.
		{"-benchtime alone", []string{"-benchtime", "20x", "."}, false},
		{"-benchtime=", []string{"-benchtime=20x", "."}, false},
		{"-benchmem alone", []string{"-benchmem", "."}, false},

		// ⛔ And an EMPTY selection is not a benchmark run: `-bench=` selects none,
		// exactly as `-run=` does.
		{"-bench= with nothing after it", []string{"-bench=", "."}, false},
		{"-bench with an empty value", []string{"-bench", "", "."}, false},
		{"-bench as the last argument", []string{".", "-bench"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, got := benchRequested(c.args); got != c.want {
				verb := "did not fire"
				if got {
					verb = "fired"
				}
				t.Errorf("the gate %s on %v, want fired=%v", verb, c.args, c.want)
			}
		})
	}
}

// The override is a REASON, never a boolean, and it has to survive being read
// out of the raw arguments in every spelling an operator will type.
func TestTheOverrideIsReadAndThenStripped(t *testing.T) {
	const why = "corpus run queued behind a CI build"
	for _, c := range []struct {
		name string
		args []string
	}{
		{"double dash, separate", []string{"-bench", ".", "--under-load", why, "."}},
		{"single dash, separate", []string{"-bench", ".", "-under-load", why, "."}},
		{"double dash, equals", []string{"-bench", ".", "--under-load=" + why, "."}},
		{"single dash, equals", []string{"-bench", ".", "-under-load=" + why, "."}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := benchRequested(c.args)
			if !ok {
				t.Fatal("the gate did not fire, so the override was never consulted")
			}
			if got != why {
				t.Errorf("reason = %q, want %q — an override whose reason is lost is a "+
					"measurement taken on a busy machine with nothing saying why", got, why)
			}
			// ⛔ And `go test` has never heard of this flag. Passing it through
			// makes every gated run die with "flag provided but not defined",
			// which reads as a broken command rather than a working gate.
			for _, a := range withoutUnderLoad(c.args) {
				if a == "--under-load" || a == "-under-load" || a == why ||
					a == "--under-load="+why || a == "-under-load="+why {
					t.Errorf("--under-load survived into the arguments go test sees: %v",
						withoutUnderLoad(c.args))
				}
			}
		})
	}

	// ⛔ THE CONTROL: stripping must not eat anything else. A run with no override
	// comes through unchanged, and one with an override keeps every real flag.
	plain := []string{"-bench", ".", "-benchtime", "20x", "-run", "^$", "./..."}
	if got := withoutUnderLoad(plain); len(got) != len(plain) {
		t.Errorf("withoutUnderLoad changed a command that has no override: %v -> %v",
			plain, got)
	}
	withFlag := []string{"-bench", ".", "--under-load", why, "-benchtime", "20x", "./..."}
	got := withoutUnderLoad(withFlag)
	if len(got) != 5 || got[0] != "-bench" || got[2] != "-benchtime" || got[4] != "./..." {
		t.Errorf("stripping the override took a real flag with it: %v -> %v", withFlag, got)
	}
}
