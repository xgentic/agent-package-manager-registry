package cli

import (
	"strings"
	"testing"
)

// resolveListenAddr is where a flag beats an environment variable, and getting
// it wrong means an unauthenticated server binds somewhere nobody asked for.
func TestResolveListenAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cfgAddr  string
		addrFlag string
		portFlag string
		want     string
	}{
		{"no flags keeps the configured address", "127.0.0.1:3000", "", "", "127.0.0.1:3000"},
		{"addr flag wins", ":3000", "127.0.0.1:8080", "", "127.0.0.1:8080"},
		{
			// The host half is the security-relevant half: -port must not
			// silently widen a loopback binding to every interface.
			"port flag replaces only the port",
			"127.0.0.1:3000", "", "8080", "127.0.0.1:8080",
		},
		{"port flag on a wildcard address", ":3000", "", "8080", ":8080"},
		{"port flag on an ipv6 host", "[::1]:3000", "", "8080", "[::1]:8080"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := resolveListenAddr(tt.cfgAddr, tt.addrFlag, tt.portFlag)
			if err != nil {
				t.Fatalf("resolveListenAddr(%q, %q, %q) error = %v, want nil",
					tt.cfgAddr, tt.addrFlag, tt.portFlag, err)
			}
			if got != tt.want {
				t.Errorf("resolveListenAddr(%q, %q, %q) = %q, want %q",
					tt.cfgAddr, tt.addrFlag, tt.portFlag, got, tt.want)
			}
		})
	}
}

func TestResolveListenAddrRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		addrFlag string
		portFlag string
		want     string
	}{
		{"both flags", "127.0.0.1:8080", "8080", "not both"},
		{"addr without a port", "127.0.0.1", "", "-addr"},
		{"addr with an unusable port", "127.0.0.1:70000", "", "-addr"},
		{"port not a number", "", "http", "-port"},
		{"port out of range", "", "70000", "-port"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := resolveListenAddr(":3000", tt.addrFlag, tt.portFlag)
			if err == nil {
				t.Fatalf("resolveListenAddr(:3000, %q, %q) error = nil, want a failure",
					tt.addrFlag, tt.portFlag)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}
