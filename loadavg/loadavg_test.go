package loadavg

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

func TestParseProc(t *testing.T) {
	v, err := parseProc("7.38 11.68 12.61 3/1234 5678\n")
	if err != nil || v != 7.38 {
		t.Fatalf("got %v, %v", v, err)
	}
	if _, err := parseProc(""); err == nil {
		t.Fatal("empty input accepted")
	}
}

func TestDecodeDarwin(t *testing.T) {
	var b bytes.Buffer
	for _, v := range []uint32{7380, 11680, 12610, 0} { // 3 fixpt + padding
		binary.Write(&b, binary.LittleEndian, v)
	}
	binary.Write(&b, binary.LittleEndian, int64(1000))
	v, err := decodeDarwin(b.Bytes())
	if err != nil || v != 7.38 {
		t.Fatalf("got %v, %v", v, err)
	}
	if _, err := decodeDarwin(make([]byte, 8)); err == nil {
		t.Fatal("short buffer accepted")
	}
	if _, err := decodeDarwin(make([]byte, 24)); err == nil {
		t.Fatal("zero fscale accepted")
	}
}

func TestAvg1ReadsThisMachine(t *testing.T) {
	v, err := Avg1()
	if err != nil {
		t.Skip(err)
	}
	if v < 0 || v > 10000 {
		t.Fatalf("implausible load %v", v)
	}
}

func seq(vals ...float64) func() (float64, error) {
	i := 0
	return func() (float64, error) {
		v := vals[i]
		if i < len(vals)-1 {
			i++
		}
		return v, nil
	}
}

func TestWaitFunc(t *testing.T) {
	waits := 0
	l, err := WaitFunc(context.Background(), seq(9, 8, 3), 4, time.Second, time.Millisecond, func(float64) { waits++ })
	if err != nil || l != 3 || waits != 2 {
		t.Fatalf("got load %v, err %v, waits %d", l, err, waits)
	}
	// Zero timeout checks once.
	l, err = WaitFunc(context.Background(), seq(9, 1), 4, 0, time.Millisecond, nil)
	if !errors.Is(err, ErrBusy) || l != 9 {
		t.Fatalf("zero timeout: %v, %v", l, err)
	}
	// Never drops: gives up at the deadline.
	_, err = WaitFunc(context.Background(), seq(9), 4, 20*time.Millisecond, 5*time.Millisecond, nil)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("want ErrBusy, got %v", err)
	}
	// A read error is not a high load.
	_, err = WaitFunc(context.Background(), func() (float64, error) { return 0, errors.New("nope") }, 4, 0, time.Millisecond, nil)
	if err == nil || errors.Is(err, ErrBusy) {
		t.Fatalf("read error: %v", err)
	}
	// Cancelled context stops the wait.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := WaitFunc(ctx, seq(9), 4, time.Minute, 10*time.Millisecond, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("want Canceled, got %v", err)
	}
}
