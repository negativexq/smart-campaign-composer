//go:build linux

// Command clockstep moves the wall clock of the machine it runs on forward at once,
// as a time service that steps instead of slewing would. The load suite runs it in a
// privileged container on Docker Desktop, where every container shares the VM's
// clock, to check what a step does to the rate limiter:
//
//	clockstep 70ms
//
// It prints how far the wall clock moved against the monotonic clock, which a step
// does not touch.
package main

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: clockstep <duration>")
		os.Exit(2)
	}
	by, err := time.ParseDuration(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	before := time.Now()
	tv := syscall.NsecToTimeval(before.Add(by).UnixNano())
	if err := syscall.Settimeofday(&tv); err != nil {
		fmt.Fprintln(os.Stderr, "set the clock:", err)
		os.Exit(1)
	}
	after := time.Now()
	// Round strips the monotonic reading, so the first difference is by the wall clock.
	wall, monotonic := after.Round(0).Sub(before.Round(0)), after.Sub(before)
	fmt.Printf("%s\n", (wall - monotonic).Round(100*time.Microsecond))
}
