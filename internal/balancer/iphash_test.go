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

func TestIPHash_TrustFlagSelectsIPSource(t *testing.T) {
	backends := []*Backend{
		mustBackend(t, "http://a.com"),
		mustBackend(t, "http://b.com"),
		mustBackend(t, "http://c.com"),
	}
	r, _ := http.NewRequest(http.MethodGet, "http://proxy/", nil)
	r.RemoteAddr = "1.1.1.1:1000"
	r.Header.Set("X-Forwarded-For", "2.2.2.2")

	// trust=false → hash the TCP peer (1.1.1.1).
	got, err := ipHash{trustForwarded: false}.Next(r, backends)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if want := backends[fnv1a32("1.1.1.1")%uint32(len(backends))]; got != want {
		t.Errorf("trust=false routed to %s, want the RemoteAddr-hashed backend %s", got, want)
	}

	// trust=true → hash the forwarded client (2.2.2.2).
	got, err = ipHash{trustForwarded: true}.Next(r, backends)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if want := backends[fnv1a32("2.2.2.2")%uint32(len(backends))]; got != want {
		t.Errorf("trust=true routed to %s, want the XFF-hashed backend %s", got, want)
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		trust      bool
		want       string
	}{
		{name: "remote addr ipv4", remoteAddr: "192.168.1.5:12345", want: "192.168.1.5"},
		{name: "remote addr ipv6", remoteAddr: "[2001:db8::1]:443", want: "2001:db8::1"},
		{name: "remote addr no port", remoteAddr: "noport", want: "noport"},
		{
			name:       "xff ignored when not trusted",
			remoteAddr: "10.0.0.1:9999",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.9"},
			trust:      false,
			want:       "10.0.0.1", // header not trusted → TCP peer
		},
		{
			name:       "xff honoured when trusted",
			remoteAddr: "10.0.0.1:9999",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.9"},
			trust:      true,
			want:       "203.0.113.9",
		},
		{
			name:       "xff leftmost of chain",
			remoteAddr: "10.0.0.1:9999",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.9, 70.41.3.18"},
			trust:      true,
			want:       "203.0.113.9",
		},
		{
			name:       "blank xff falls back to real-ip",
			remoteAddr: "10.0.0.1:9999",
			headers:    map[string]string{"X-Forwarded-For": " , ", "X-Real-IP": "198.51.100.4"},
			trust:      true,
			want:       "198.51.100.4",
		},
		{
			name:       "empty xff falls back to remote addr",
			remoteAddr: "10.0.0.1:9999",
			headers:    map[string]string{"X-Forwarded-For": ""},
			trust:      true,
			want:       "10.0.0.1",
		},
		{
			name:       "xff preferred over real-ip",
			remoteAddr: "10.0.0.1:9999",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.9", "X-Real-IP": "198.51.100.4"},
			trust:      true,
			want:       "203.0.113.9",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Request{RemoteAddr: tt.remoteAddr, Header: http.Header{}}
			for k, v := range tt.headers {
				r.Header.Set(k, v)
			}
			if got := clientIP(r, tt.trust); got != tt.want {
				t.Errorf("clientIP(trust=%v) = %q, want %q", tt.trust, got, tt.want)
			}
		})
	}
	if got := clientIP(nil, true); got != "" {
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
