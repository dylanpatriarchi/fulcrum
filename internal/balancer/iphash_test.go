package balancer

import (
	"net/http"
	"testing"
)

func reqFromIP(t *testing.T, ip string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodGet, "http://proxy/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	r.RemoteAddr = ip + ":54321"
	return r
}

func TestIPHash_EmptyCandidates(t *testing.T) {
	if _, err := (ipHash{}).Next(reqFromIP(t, "1.2.3.4"), nil); err != ErrNoHealthyBackends {
		t.Errorf("Next(empty) error = %v, want ErrNoHealthyBackends", err)
	}
}

func TestIPHash_StickyPerClient(t *testing.T) {
	backends := []*Backend{
		mustBackend(t, "http://a.com"),
		mustBackend(t, "http://b.com"),
		mustBackend(t, "http://c.com"),
	}
	h := ipHash{}

	// The same client IP must always map to the same backend.
	first, err := h.Next(reqFromIP(t, "203.0.113.7"), backends)
	if err != nil {
		t.Fatalf("Next(): %v", err)
	}
	for i := 0; i < 50; i++ {
		got, err := h.Next(reqFromIP(t, "203.0.113.7"), backends)
		if err != nil {
			t.Fatalf("Next() #%d: %v", i, err)
		}
		if got != first {
			t.Fatalf("ip-hash not sticky: pick #%d = %s, first = %s", i, got, first)
		}
	}
}

func TestIPHash_DistributesAcrossClients(t *testing.T) {
	backends := []*Backend{
		mustBackend(t, "http://a.com"),
		mustBackend(t, "http://b.com"),
		mustBackend(t, "http://c.com"),
	}
	h := ipHash{}

	seen := map[*Backend]bool{}
	// A spread of distinct client IPs should reach more than one backend.
	for i := 0; i < 256; i++ {
		ip := "10.0.0." + itoa(i)
		b, err := h.Next(reqFromIP(t, ip), backends)
		if err != nil {
			t.Fatalf("Next(): %v", err)
		}
		seen[b] = true
	}
	if len(seen) < 2 {
		t.Errorf("ip-hash mapped 256 distinct IPs to only %d backend(s)", len(seen))
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		remoteAddr string
		want       string
	}{
		{"192.168.1.5:12345", "192.168.1.5"},
		{"[2001:db8::1]:443", "2001:db8::1"},
		{"noport", "noport"}, // fallback when SplitHostPort fails
	}
	for _, tt := range tests {
		r := &http.Request{RemoteAddr: tt.remoteAddr}
		if got := clientIP(r); got != tt.want {
			t.Errorf("clientIP(%q) = %q, want %q", tt.remoteAddr, got, tt.want)
		}
	}
	if got := clientIP(nil); got != "" {
		t.Errorf("clientIP(nil) = %q, want empty", got)
	}
}

// itoa is a tiny, allocation-light integer-to-string for test IP construction.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [3]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
