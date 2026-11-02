//go:build !darwin

package loadavg

import "errors"

func darwinAvg1() (float64, error) { return 0, errors.New("not darwin") }
