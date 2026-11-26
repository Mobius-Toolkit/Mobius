package engine

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
)

func TestNetworkErrorFindsTheLossOfTheNetworkInTheChainOfAnError(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		err  error
		want bool
	}{
		"a failed DNS lookup":    {fmt.Errorf("read the issue: %w", &net.DNSError{Err: "no such host", Name: "api.github.com"}), true},
		"a refused connection":   {fmt.Errorf("read the issue: %w", syscall.ECONNREFUSED), true},
		"a reset connection":     {fmt.Errorf("read the issue: %w", syscall.ECONNRESET), true},
		"a failed dial":          {fmt.Errorf("read the issue: %w", &net.OpError{Op: "dial", Err: errors.New("i/o timeout")}), true},
		"a failed read":          {fmt.Errorf("read the issue: %w", &net.OpError{Op: "read", Err: errors.New("broken pipe")}), false},
		"ENOTFOUND of an agent":  {errors.New(`{"code":-32603,"message":"getaddrinfo ENOTFOUND api.github.com"}`), true},
		"ECONNREFUSED of agent":  {errors.New("connect ECONNREFUSED 127.0.0.1:443"), true},
		"ECONNRESET of an agent": {errors.New("read ECONNRESET"), true},
		"another error":          {errors.New("peer disconnected"), false},
	} {
		if got := networkError(test.err); got != test.want {
			t.Errorf("%s: networkError = %v", name, got)
		}
	}
}
