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
		name       string
		remoteAddr string
		headers    map[string]string
		want       string
	}{
		{name: "remote addr ipv4", remoteAddr: "192.168.1.5:12345", want: "192.168.1.5"},
		{name: "remote addr ipv6", remoteAddr: "[2001:db8::1]:443", want: "2001:db8::1"},
		{name: "remote addr no port", remoteAddr: "noport", want: "noport"},
		{
			name:       "x-forwarded-for wins",
			remoteAddr: "10.0.0.1:9999",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.9"},
			want:       "203.0.113.9",
		},
		{
			name:       "x-forwarded-for leftmost of chain",
			remoteAddr: "10.0.0.1:9999",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.9, 70.41.3.18, 150.172.238.178"},
			want:       "203.0.113.9",
		},
		{
			name:       "x-real-ip fallback",
			remoteAddr: "10.0.0.1:9999",
			headers:    map[string]string{"X-Real-IP": "198.51.100.4"},
			want:       "198.51.100.4",
		},
		{
			name:       "x-forwarded-for preferred over x-real-ip",
			remoteAddr: "10.0.0.1:9999",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.9", "X-Real-IP": "198.51.100.4"},
			want:       "203.0.113.9",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Request{RemoteAddr: tt.remoteAddr, Header: http.Header{}}
			for k, v := range tt.headers {
				r.Header.Set(k, v)
			}
			if got := clientIP(r); got != tt.want {
				t.Errorf("clientIP() = %q, want %q", got, tt.want)
			}
		})
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
