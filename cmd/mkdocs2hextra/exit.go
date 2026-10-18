// Copyright (c) 2026, the go-fleettools authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// The exit-status convention of this repository, in the five lines this one
// command needs:
//
//	0  the question was answered and the answer is clean
//	1  the question was answered and something was found
//	2  the question could NOT be answered
//
// ⛔ A LANE THAT CANNOT TELL 1 FROM 2 eventually reads a conversion that could
// not run as a site with nothing to convert.
//
// ⛔ AND THIS IS THE THIRTIETH COPY OF THAT CONVENTION IN THIS REPOSITORY,
// which is a defect I am adding on purpose rather than by accident: a session
// in another checkout is migrating all 29 commands onto cobra behind an
// internal/cli that holds exactly this, and that work is neither pushed nor
// claimed. Depending on it would make this command red until it lands;
// committing it under my own name would take their work. So this is
// deliberately the smallest possible duplicate, and it should be DELETED in
// favour of internal/cli the moment that migration is merged.
type exit struct {
	code int
	err  error
}

func (e *exit) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit status %d", e.code)
	}
	return e.err.Error()
}

func (e *exit) Unwrap() error { return e.err }

func found(format string, a ...any) error {
	return &exit{code: 1, err: fmt.Errorf(format, a...)}
}

func unanswerable(format string, a ...any) error {
	return &exit{code: 2, err: fmt.Errorf(format, a...)}
}

// run executes the command and ends the process with the status its error
// carries.
func runMain(c *cobra.Command) {
	err := c.Execute()
	if err == nil {
		return
	}
	var e *exit
	if errors.As(err, &e) {
		if e.err != nil {
			fmt.Fprintf(c.ErrOrStderr(), "⛔ %v\n", e.err)
		}
		os.Exit(e.code)
	}
	// cobra's own errors — an unknown flag, the wrong number of arguments —
	// are questions that could not be answered.
	fmt.Fprintf(c.ErrOrStderr(), "⛔ %v\n", err)
	os.Exit(2)
}
