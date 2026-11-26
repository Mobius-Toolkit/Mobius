package api

import (
	"context"
	"time"
)

// Network is the state of the network pause.
type Network struct {
	// LostAt is the time when Mobius lost the network, or null while the network works. While it is set, no session and no Worker restart starts
	LostAt *time.Time `gork:"lostAt"`
}

// GetNetworkRequest is the request of GetNetwork.
type GetNetworkRequest struct{}

// GetNetworkResponse is the response of GetNetwork.
type GetNetworkResponse struct {
	Body Envelope[Network]
}

// GetNetwork returns the state of the network pause.
func (h *handlers) GetNetwork(ctx context.Context, _ GetNetworkRequest) (*GetNetworkResponse, error) {
	network, err := h.network(ctx)
	if err != nil {
		return nil, err
	}
	return &GetNetworkResponse{Body: Envelope[Network]{Data: network}}, nil
}

func (h *handlers) network(ctx context.Context) (Network, error) {
	lostAt, err := h.engine.NetworkLostAt(ctx)
	return Network{LostAt: lostAt}, err
}
