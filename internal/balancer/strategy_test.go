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

func TestNames_RegistersAllStrategies(t *testing.T) {
	want := []string{"ip-hash", "least-connections", "random", "round-robin", "weighted"}
	got := Names()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Names() = %v, want %v (sorted, complete set)", got, want)
	}
	// Every registered name must construct without error.
	for _, n := range got {
		if _, err := New(n); err != nil {
			t.Errorf("New(%q): %v", n, err)
		}
	}
}
