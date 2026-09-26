package api

import (
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	cases := []struct{ remote, xff, want string }{
		{"203.0.113.5:1234", "1.2.3.4", "203.0.113.5"},              // public peer: header ignored
		{"172.17.0.1:1234", "", "172.17.0.1"},                       // no proxy header
		{"172.17.0.1:1234", "198.51.100.7", "198.51.100.7"},         // behind a local proxy
		{"127.0.0.1:1234", "6.6.6.6, 198.51.100.7", "198.51.100.7"}, // spoofed first entry is ignored
		{"127.0.0.1:1234", "garbage", "127.0.0.1"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := ClientIP(r); got != c.want {
			t.Errorf("ClientIP(%s, %q) = %s, want %s", c.remote, c.xff, got, c.want)
		}
	}
}
