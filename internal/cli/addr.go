package cli

import (
	"errors"
	"fmt"
	"net"

	"github.com/xgentic/agent-package-manager-registry/internal/config"
)

// resolveListenAddr applies the serve flags on top of the configured address.
//
// -port keeps the configured host and changes only the port. That matters:
// a service pinned to 127.0.0.1 must not become world-reachable because the
// operator asked for a different port.
func resolveListenAddr(cfgAddr, addrFlag, portFlag string) (string, error) {
	if addrFlag != "" && portFlag != "" {
		return "", errors.New("-addr and -port both set: use one, not both")
	}

	switch {
	case addrFlag != "":
		if err := config.ValidateAddr(addrFlag); err != nil {
			return "", fmt.Errorf("-addr: %w", err)
		}
		return addrFlag, nil

	case portFlag != "":
		if err := config.ValidatePort(portFlag); err != nil {
			return "", fmt.Errorf("-port: %w", err)
		}
		host, _, err := net.SplitHostPort(cfgAddr)
		if err != nil {
			return "", fmt.Errorf("configured address %q is not host:port: %w", cfgAddr, err)
		}
		return net.JoinHostPort(host, portFlag), nil

	default:
		return cfgAddr, nil
	}
}
