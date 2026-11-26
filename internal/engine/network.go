package engine

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net"
	"slices"
	"strings"
	"syscall"
	"time"
)

// networkTexts are the texts of a network error that an agent reports as a string.
var networkTexts = []string{"ENOTFOUND", "ECONNREFUSED", "ECONNRESET"}

// networkError tells if err is the loss of the network: a failed DNS lookup, a refused or reset connection, a failed
// dial, or an agent error with the text of one of these.
func networkError(err error) bool {
	var dns *net.DNSError
	var op *net.OpError
	switch {
	case errors.As(err, &dns), errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.ECONNRESET):
		return true
	case errors.As(err, &op) && op.Op == "dial":
		return true
	}
	return slices.ContainsFunc(networkTexts, func(text string) bool { return strings.Contains(err.Error(), text) })
}

// NetworkLostAt gives the time when the network pause started, or nil when the network works.
func (e *Engine) NetworkLostAt(ctx context.Context) (*time.Time, error) {
	text, err := e.queries.GetNetworkPause(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lostAt, err := time.Parse(time.RFC3339Nano, text)
	return &lostAt, err
}

// loseNetwork starts the network pause. A pause that holds keeps its first time.
func (e *Engine) loseNetwork(ctx context.Context) error {
	added, err := e.queries.AddNetworkPause(ctx, now())
	if err != nil || added == 0 {
		return err
	}
	log.Printf("the network is lost: no session starts until GitHub answers")
	e.publish(Change{Network: true})
	return nil
}

// checkNetwork ends the network pause when GitHub answers. Each answer of GitHub counts, also an error status.
func (e *Engine) checkNetwork(ctx context.Context) {
	lostAt, err := e.NetworkLostAt(ctx)
	if err != nil || lostAt == nil {
		if err != nil {
			log.Printf("read the network pause: %v", err)
		}
		return
	}
	if err := e.github.Reach(ctx); err != nil && networkError(err) {
		return
	}
	if _, err := e.queries.DeleteNetworkPause(ctx); err != nil {
		log.Printf("end the network pause: %v", err)
		return
	}
	log.Printf("the network works again")
	e.publish(Change{Network: true})
	e.networkChanged.notify()
}

// waitForNetwork holds until the network pause ends.
func (e *Engine) waitForNetwork(ctx context.Context) error {
	for {
		changed := e.networkChanged.wait()
		lostAt, err := e.NetworkLostAt(ctx)
		if err != nil || lostAt == nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}
