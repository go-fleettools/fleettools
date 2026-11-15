//go:build race

package paritytext

// raceOn: the race detector slows regexp-heavy code several times over, so the
// absolute time budget of TestScanIsNotQuadratic is scaled; the scaling test
// (4x the text, under 10x the time) is what catches a quadratic return there.
const raceOn = true
