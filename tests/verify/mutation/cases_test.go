package mutation

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The mutation check runs by hand, so nothing else notices when code it breaks moves
// or a test it relies on is renamed. This reads each case without breaking anything,
// fast enough for every `make test`.
func TestEveryCaseStillPointsAtItsCodeAndTest(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	read := func(path string) string {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatalf("%v", err)
		}
		return string(body)
	}
	names := map[string]bool{}
	for _, c := range cases {
		if names[c.Name] {
			t.Errorf("%s: two cases share this name", c.Name)
		}
		names[c.Name] = true
		if n := strings.Count(read(c.File), c.Find); n != 1 {
			t.Errorf("%s: the code to break appears %d times in %s, want once", c.Name, n, c.File)
		}
		switch {
		case c.Go != nil:
			name := strings.TrimSuffix(strings.TrimPrefix(c.Go.Run, "^"), "$")
			if !goTestExists(t, filepath.Join(root, c.Go.Package), name) {
				t.Errorf("%s: no func %s in %s", c.Name, name, c.Go.Package)
			}
		case c.Frontend != nil:
			body := read(filepath.Join("frontend", c.Frontend.File))
			// vitest's -t keeps tests whose name contains the given text.
			if !regexp.MustCompile(`\b(it|test)\(\s*["'` + "`" + `][^"'` + "`" + `]*` + regexp.QuoteMeta(c.Frontend.Name)).MatchString(body) {
				t.Errorf("%s: no test name containing %q in frontend/%s", c.Name, c.Frontend.Name, c.Frontend.File)
			}
		default:
			t.Errorf("%s: names no test", c.Name)
		}
	}
}

func goTestExists(t *testing.T, dir, name string) bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	declared := regexp.MustCompile(`(?m)^func ` + regexp.QuoteMeta(name) + `\(t \*testing\.T\)`)
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if declared.Match(body) {
			return true
		}
	}
	return false
}
