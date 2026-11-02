// Package loadavg reads the host's 1-minute load average and waits for it to
// drop, so that a benchmark is not measured on a busy machine.
//
// Linux reads /proc/loadavg (proc(5)). Darwin reads sysctl vm.loadavg, a
// struct loadavg { fixpt_t ldavg[3]; long fscale; } (xnu sys/resource.h).
// Every other platform gets a clear error, never a zero.
package loadavg

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ErrBusy is wrapped by the error Wait returns when the load stayed above the
// limit for the whole timeout.
var ErrBusy = errors.New("load average above the limit")

// Avg1 returns the 1-minute load average.
func Avg1() (float64, error) {
	switch runtime.GOOS {
	case "linux":
		b, err := os.ReadFile("/proc/loadavg")
		if err != nil {
			return 0, err
		}
		return parseProc(string(b))
	case "darwin":
		return darwinAvg1()
	}
	return 0, errors.New("load average not supported on " + runtime.GOOS)
}

// Wait returns the current 1-minute load once it is at most max. With a zero
// timeout it checks once. Otherwise it polls every poll until timeout has
// elapsed, then gives up with an error wrapping ErrBusy. onWait, when not nil,
// is called with each reading that is too high before sleeping.
func Wait(ctx context.Context, max float64, timeout, poll time.Duration, onWait func(float64)) (float64, error) {
	return WaitFunc(ctx, Avg1, max, timeout, poll, onWait)
}

// WaitFunc is Wait over a replaceable source of readings.
func WaitFunc(ctx context.Context, read func() (float64, error), max float64, timeout, poll time.Duration, onWait func(float64)) (float64, error) {
	deadline := time.Now().Add(timeout)
	for {
		l, err := read()
		if err != nil {
			return 0, fmt.Errorf("cannot read the load average: %w", err)
		}
		if l <= max {
			return l, nil
		}
		if timeout <= 0 || time.Now().Add(poll).After(deadline) {
			return l, fmt.Errorf("%w: %.2f > %.2f", ErrBusy, l, max)
		}
		if onWait != nil {
			onWait(l)
		}
		select {
		case <-ctx.Done():
			return l, ctx.Err()
		case <-time.After(poll):
		}
	}
}

func parseProc(s string) (float64, error) {
	f := strings.Fields(s)
	if len(f) < 1 {
		return 0, errors.New("empty /proc/loadavg")
	}
	return strconv.ParseFloat(f[0], 64)
}

// decodeDarwin decodes sysctl vm.loadavg on a 64-bit little-endian Mac: three
// uint32, 4 bytes of padding, an int64 fscale.
func decodeDarwin(b []byte) (float64, error) {
	if len(b) < 24 {
		return 0, errors.New("vm.loadavg too short")
	}
	ld0 := binary.LittleEndian.Uint32(b[0:4])
	fscale := int64(binary.LittleEndian.Uint64(b[16:24]))
	if fscale <= 0 {
		return 0, errors.New("vm.loadavg: bad fscale")
	}
	return float64(ld0) / float64(fscale), nil
}
