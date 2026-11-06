//go:build verify

// Package mutation checks that the tests catch broken code. Each case puts one
// deliberate fault into the source, runs the tests written for that code and expects
// them to fail, then puts the source back. A case whose tests still pass means those
// tests would let that fault ship.
package mutation

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Case is one deliberate fault and the test expected to catch it.
type Case struct {
	Name     string
	File     string // relative to the repository root
	Find     string
	Replace  string
	Go       *GoTest
	Frontend *FrontendTest
}

// GoTest runs `go test -run Run Package`.
type GoTest struct{ Package, Run string }

// FrontendTest runs one vitest file, filtered by test name.
type FrontendTest struct{ File, Name string }

const caseTimeout = 10 * time.Minute

func TestEveryFaultIsCaught(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	// Every case must match before any file changes, so a source left broken by an
	// earlier, interrupted run shows up here instead of being mutated again.
	for _, c := range cases {
		source, err := os.ReadFile(filepath.Join(root, c.File))
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if n := strings.Count(string(source), c.Find); n != 1 {
			t.Fatalf("%s: the code to break appears %d times in %s, want once. Update the case, or restore the file if a run was interrupted.", c.Name, n, c.File)
		}
	}
	// An interrupt still puts the source back before the run stops.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	for _, c := range cases {
		if ctx.Err() != nil {
			t.Fatal("interrupted")
		}
		t.Run(c.Name, func(t *testing.T) { check(ctx, t, root, c) })
	}
}

func check(ctx context.Context, t *testing.T, root string, c Case) {
	path := filepath.Join(root, c.File)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(original), c.Find, c.Replace, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.WriteFile(path, original, 0o644); err != nil {
			t.Errorf("restore %s: %v", c.File, err)
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, caseTimeout)
	defer cancel()
	var cmd *exec.Cmd
	switch {
	case c.Go != nil:
		cmd = exec.CommandContext(ctx, "go", "test", "-count=1", "-run", c.Go.Run, c.Go.Package)
		cmd.Dir = root
	case c.Frontend != nil:
		cmd = exec.CommandContext(ctx, "npx", "vitest", "run", c.Frontend.File, "-t", c.Frontend.Name)
		cmd.Dir = filepath.Join(root, "frontend")
	default:
		t.Fatal("the case names no test")
	}
	output, err := cmd.CombinedOutput()
	out := string(output)
	failedTest := strings.Contains(out, "--- FAIL") || (strings.Contains(out, "Tests") && strings.Contains(out, " failed"))
	switch {
	case ctx.Err() != nil:
		t.Fatalf("the tests did not finish: %v", ctx.Err())
	case err == nil:
		t.Errorf("not caught: the tests still pass with this fault in %s", c.File)
	case !failedTest:
		// A fault that stops the build proves nothing about the tests.
		t.Errorf("the fault broke the build instead of failing a test:\n%s", tail(out))
	}
}

func tail(s string) string {
	if len(s) > 2000 {
		return s[len(s)-2000:]
	}
	return s
}
