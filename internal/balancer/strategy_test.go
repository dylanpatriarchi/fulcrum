package balancer

import (
	"reflect"
	"testing"
)

func TestNew_KnownAndUnknown(t *testing.T) {
	s, err := New("round-robin")
	if err != nil {
		t.Fatalf("New(round-robin): %v", err)
	}
	if s.Name() != "round-robin" {
		t.Errorf("Name() = %q, want round-robin", s.Name())
	}

	if _, err := New("does-not-exist"); err == nil {
		t.Error("New(unknown) = nil error, want error")
	}
}

func TestNew_ReturnsIndependentInstances(t *testing.T) {
	a, _ := New("round-robin")
	b, _ := New("round-robin")
	if a == b {
		t.Error("New should return a fresh instance each call (independent cursor state)")
	}
}

func TestNames_IncludesRoundRobin(t *testing.T) {
	names := Names()
	found := false
	for _, n := range names {
		if n == "round-robin" {
			found = true
		}
	}
	if !found {
		t.Errorf("Names() = %v, want to include round-robin", names)
	}
	// Names must be sorted.
	sorted := make([]string, len(names))
	copy(sorted, names)
	if !reflect.DeepEqual(names, sortedCopy(names)) {
		t.Errorf("Names() = %v, want sorted", names)
	}
}

func sortedCopy(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
