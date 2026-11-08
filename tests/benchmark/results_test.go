package benchmark

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// An old report read as a current one would show problems that were fixed long ago,
// so every report is either listed as current in the index or says it is superseded.
func TestEveryBenchmarkReportIsCurrentOrMarkedSuperseded(t *testing.T) {
	t.Parallel()
	index, err := os.ReadFile("results/README.md")
	if err != nil {
		t.Fatal(err)
	}
	current, history := linkedReports(t, string(index))
	reports, err := filepath.Glob("results/*.md")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]int{}
	for _, path := range reports {
		name := filepath.Base(path)
		if name == "README.md" {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		cases[name] = caseCount(string(body))
		marked := strings.HasPrefix(strings.TrimSpace(strings.SplitN(string(body), "\n", 2)[1]), "> **Superseded.**")
		switch {
		case current[name] && marked:
			t.Errorf("%s is listed as current but says it is superseded", name)
		case current[name] && history[name]:
			t.Errorf("%s is listed as both current and history", name)
		case !current[name] && !marked:
			t.Errorf("%s is not listed as current and does not say it is superseded; see results/README.md", name)
		case !current[name] && !history[name]:
			t.Errorf("%s is superseded but missing from the history in results/README.md", name)
		}
	}
	// A partial run may come after the current report; a run of at least as many
	// cases is a new result for that model and has to be chosen one way or the other.
	for name := range current {
		for other, count := range cases {
			if other > name && modelOf(other) == modelOf(name) && count >= cases[name] {
				t.Errorf("%s is listed as current, but %s is newer and has as many cases; make it current or say why not", name, other)
			}
		}
	}
	for name := range history {
		if _, err := os.Stat(filepath.Join("results", name)); err != nil {
			t.Errorf("the index lists %s, which does not exist", name)
		}
	}
}

var reportLink = regexp.MustCompile(`\]\(([0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{4}-[^)]+\.md)\)`)

func linkedReports(t *testing.T, index string) (current, history map[string]bool) {
	t.Helper()
	current, history = map[string]bool{}, map[string]bool{}
	section := ""
	for _, line := range strings.Split(index, "\n") {
		if strings.HasPrefix(line, "## ") {
			section = strings.TrimPrefix(line, "## ")
			continue
		}
		for _, match := range reportLink.FindAllStringSubmatch(line, -1) {
			switch section {
			case "Current":
				current[match[1]] = true
			case "History":
				history[match[1]] = true
			}
		}
	}
	if len(current) == 0 {
		t.Fatal("results/README.md lists no current report")
	}
	return current, history
}

var casesLine = regexp.MustCompile(`(?m)^[0-9-]+ [0-9:]+, [0-9]+ runs? of ([0-9]+) cases\.`)

// caseCount reads "1 runs of 247 cases." from a report's heading.
func caseCount(body string) int {
	match := casesLine.FindStringSubmatch(body)
	if match == nil {
		return 0
	}
	count, _ := strconv.Atoi(match[1])
	return count
}

// modelOf is the part of a report's name after its date and time.
func modelOf(name string) string {
	return strings.TrimSuffix(name[len("2006-01-02-1504-"):], ".md")
}
