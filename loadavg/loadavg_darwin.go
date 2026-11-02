//go:build darwin

package loadavg

import "syscall"

// darwinAvg1 reads sysctl vm.loadavg.
func darwinAvg1() (float64, error) {
	raw, err := syscall.Sysctl("vm.loadavg")
	if err != nil {
		return 0, err
	}
	// syscall.Sysctl drops a trailing NUL byte; pad back to the struct size.
	b := []byte(raw)
	for len(b) < 24 {
		b = append(b, 0)
	}
	return decodeDarwin(b)
}
