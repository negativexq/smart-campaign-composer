//go:build verify

// Package mutation checks that the tests catch broken code. Each case puts one
// deliberate fault into the source, runs the tests written for that code and expects
// them to fail, then puts the source back. A case whose tests still pass means those
// tests would let that fault ship.
package mutation

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

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
	// An interrupt or a stop still puts the source back before the run ends.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	clean := map[string]bool{} // test commands already seen passing on the clean source
	for _, c := range cases {
		if ctx.Err() != nil {
			t.Fatal("interrupted")
		}
		t.Run(c.Name, func(t *testing.T) { check(ctx, t, root, c, clean) })
	}
}

// check first runs the case's tests on the clean source: they must run at least
// one test and pass, or a failure later would prove nothing. Then it breaks the
// code, expects a test to fail, and puts the code back.
func check(ctx context.Context, t *testing.T, root string, c Case, clean map[string]bool) {
	key := fmt.Sprintf("%+v %+v", c.Go, c.Frontend)
	if !clean[key] {
		out, err := runTests(ctx, root, c)
		switch {
		case ctx.Err() != nil:
			t.Fatalf("interrupted: %v", ctx.Err())
		case err != nil:
			t.Fatalf("the tests fail on the clean source, so they cannot judge a fault:\n%s", tail(out))
		case ranNothing(c, out):
			t.Fatalf("the case selects no test:\n%s", tail(out))
		}
		clean[key] = true
	}

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

	out, err := runTests(ctx, root, c)
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

// runTests runs the tests a case names and returns their output.
func runTests(ctx context.Context, root string, c Case) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, caseTimeout)
	defer cancel()
	var cmd *exec.Cmd
	switch {
	case c.Go != nil:
		cmd = exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-run", c.Go.Run, c.Go.Package)
		cmd.Dir = root
	case c.Frontend != nil:
		cmd = exec.CommandContext(ctx, "npx", "vitest", "run", c.Frontend.File, "-t", c.Frontend.Name)
		cmd.Dir = filepath.Join(root, "frontend")
	default:
		return "", fmt.Errorf("the case names no test")
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}

var vitestPassed = regexp.MustCompile(`Tests\s+(\d+) passed`)

// ranNothing tells whether a passing run ran no test at all.
func ranNothing(c Case, out string) bool {
	if c.Go != nil {
		return !strings.Contains(out, "--- PASS")
	}
	match := vitestPassed.FindStringSubmatch(out)
	return match == nil || match[1] == "0"
}

func tail(s string) string {
	if len(s) > 2000 {
		return s[len(s)-2000:]
	}
	return s
}
