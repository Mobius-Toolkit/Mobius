package mcp

import (
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// reservePorts asks the route of key for count ports and gives the status and the body.
func reservePorts(t *testing.T, addr, key, count string) (int, string) {
	t.Helper()
	status, body := send(t, http.MethodPost, PortURL(addr, key)+"?count="+count, "", "")
	return status, string(body)
}

// portsOf asks the route of key for count ports and gives them.
func portsOf(t *testing.T, addr, key string, count int) []int {
	t.Helper()
	status, body := reservePorts(t, addr, key, strconv.Itoa(count))
	if status != http.StatusOK {
		t.Fatalf("%d ports: %d %s", count, status, body)
	}
	var ports []int
	for line := range strings.Lines(body) {
		port, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil {
			t.Fatalf("body = %q", body)
		}
		ports = append(ports, port)
	}
	if len(ports) != count {
		t.Fatalf("body = %q, want %d ports", body, count)
	}
	return ports
}

func requireFreeRange(t *testing.T, ports []int) {
	t.Helper()
	seen := map[int]bool{}
	for _, port := range ports {
		if port < firstPort || port > lastPort || seen[port] {
			t.Errorf("ports = %v", ports)
		}
		seen[port] = true
		listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			t.Errorf("port %d: %v", port, err)
			continue
		}
		_ = listener.Close()
	}
}

func TestThePortsRouteGivesOnePortOrTheNumberOfPortsOfTheCount(t *testing.T) {
	server, addr := start(t)
	key := server.OpenPorts()

	one := portsOf(t, addr, key, 1)
	three := portsOf(t, addr, key, 3)

	requireFreeRange(t, append(one, three...))
}

func TestTwoKeysThatHoldPortsGetDifferentPorts(t *testing.T) {
	server, addr := start(t)
	first, second := server.OpenPorts(), server.Open(Caller{})

	got := append(portsOf(t, addr, first, 2), portsOf(t, addr, second, 2)...)

	requireFreeRange(t, got)
}

func TestAPortThatAKeyHoldsGoesToNoOtherKeyUntilCloseAndThenToTheNextKey(t *testing.T) {
	server, addr := start(t)
	first, second := server.OpenPorts(), server.OpenPorts()
	held := portsOf(t, addr, first, 2)

	if got := portsOf(t, addr, second, 1); slices.Contains(held, got[0]) {
		t.Errorf("the second key got the port %d of the first key", got[0])
	}
	server.Close(first)
	third := server.OpenPorts()

	if got := portsOf(t, addr, third, 1); got[0] != held[0] {
		t.Errorf("the third key got the port %d, want the released port %d", got[0], held[0])
	}
}

func TestAPortThatAnotherProcessHoldsGoesToNoKey(t *testing.T) {
	server, addr := start(t)
	opened := server.OpenPorts()
	first := portsOf(t, addr, opened, 1)[0]
	server.Close(opened)
	listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(first))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	got := portsOf(t, addr, server.OpenPorts(), 1)

	if got[0] == first {
		t.Errorf("port %d is bound, but the server gave it", first)
	}
}

func TestABadCountGivesBadRequestAndReservesNoPort(t *testing.T) {
	server, addr := start(t)
	key := server.OpenPorts()

	for _, count := range []string{"", "0", "-1", "two"} {
		if status, body := reservePorts(t, addr, key, count); status != http.StatusBadRequest {
			t.Errorf("count %q: %d %s", count, status, body)
		}
	}

	if ports := server.callers[key].ports; len(ports) != 0 {
		t.Errorf("ports = %v", ports)
	}
}

func TestARangeWithTooFewFreePortsGivesServiceUnavailableAndReservesNoPort(t *testing.T) {
	server, addr := start(t)
	key := server.OpenPorts()
	portsOf(t, addr, key, 1)

	status, body := reservePorts(t, addr, key, strconv.Itoa(lastPort-firstPort+1))

	if want := fmt.Sprintf("the range %d-%d has no %d free ports\n", firstPort, lastPort, lastPort-firstPort+1); status != http.StatusServiceUnavailable || body != want {
		t.Errorf("%d %q, want %q", status, body, want)
	}
	if ports := server.callers[key].ports; len(ports) != 1 {
		t.Errorf("ports = %v", ports)
	}
}

func TestAnUnknownOrClosedKeyGetsNoPorts(t *testing.T) {
	server, addr := start(t)
	closed := server.OpenPorts()
	server.Close(closed)

	for _, key := range []string{"unknown", closed} {
		if status, body := reservePorts(t, addr, key, "1"); status != http.StatusNotFound {
			t.Errorf("key %q: %d %s", key, status, body)
		}
	}
}

func TestAKeyOfOpenPortsGetsPortsAndNothingElse(t *testing.T) {
	server, addr := start(t)
	key := server.OpenPorts()

	portsOf(t, addr, key, 1)

	for url, method := range map[string]string{
		URL(addr, key):        http.MethodPost,
		GHTokenURL(addr, key): http.MethodGet,
	} {
		if status, body := send(t, method, url, "2026-07-28", `{}`); status != http.StatusNotFound {
			t.Errorf("%s: %d %s", url, status, body)
		}
	}
}
