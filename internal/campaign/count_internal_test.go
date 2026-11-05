package campaign

import "testing"

func TestPeopleAreCountedAsThePagesShowThem(t *testing.T) {
	t.Parallel()
	for count, want := range map[int]string{0: "0 people", 1: "1 person", 229: "229 people", 7000: "7,000 people", 1234567: "1,234,567 people"} {
		if got := peopleCount(count); got != want {
			t.Errorf("%d: got %q, want %q", count, got, want)
		}
	}
}
