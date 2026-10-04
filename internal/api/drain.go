package api

import (
	"context"
	"net/http"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/mobius-go/internal/engine"
)

// Drain is the state of the drain before an upgrade.
type Drain struct {
	// On is true while the drain holds the new Workers. A completed drain stays on until a cancel or the restart
	On bool `gork:"on"`
	// Waiting is the number of agents that the drain waits for
	Waiting int64 `gork:"waiting"`
}

// DrainEnd tells how a drain ended.
type DrainEnd struct {
	// End is drained when no agent of Mobius runs, and cancelled when the Owner cancelled the drain
	End string `gork:"end" validate:"oneof=drained cancelled"`
}

// Upgrade is the state of the last upgrade.
type Upgrade struct {
	// Failure is the error of the last upgrade. It is empty when the last upgrade has no error
	Failure string `gork:"failure"`
}

// GetDrainRequest is the request of GetDrain.
type GetDrainRequest struct{}

// GetDrainResponse is the response of GetDrain.
type GetDrainResponse struct {
	Body Envelope[Drain]
}

// GetDrain returns the state of the drain.
func (h *handlers) GetDrain(_ context.Context, _ GetDrainRequest) (*GetDrainResponse, error) {
	return &GetDrainResponse{Body: Envelope[Drain]{Data: drainOf(h.engine.Draining())}}, nil
}

// StartDrainRequest is the request of StartDrain.
type StartDrainRequest struct{}

// DrainEndResponse is the response of StartDrain and of StartUpgrade.
type DrainEndResponse struct {
	Body Envelope[DrainEnd]
}

// StartDrain holds each new Worker in the queue, and returns when no agent runs or when the Owner cancels the drain.
func (h *handlers) StartDrain(ctx context.Context, _ StartDrainRequest) (*DrainEndResponse, error) {
	end, err := h.engine.Drain(ctx)
	if err != nil {
		return nil, err
	}
	return &DrainEndResponse{Body: Envelope[DrainEnd]{Data: DrainEnd{End: string(end)}}}, nil
}

// CancelDrainRequest is the request of CancelDrain.
type CancelDrainRequest struct{}

// CancelDrain ends the drain, so the held Workers start. It does nothing while the upgrade replaces the program.
func (h *handlers) CancelDrain(_ context.Context, _ CancelDrainRequest) error {
	h.engine.CancelDrain()
	return nil
}

// StartUpgradeRequest is the request of StartUpgrade.
type StartUpgradeRequest struct{}

// StartUpgrade downloads the newest release, drains the agents, and restarts Mobius with the new release. It returns
// drained when the new release starts, and cancelled when the Owner cancels the drain. The upgrade goes on when the
// client closes the request.
func (h *handlers) StartUpgrade(ctx context.Context, _ StartUpgradeRequest) (*DrainEndResponse, error) {
	end, err := h.engine.Upgrade(ctx)
	if engine.Refused(err) {
		return nil, api.NewHTTPError(http.StatusConflict, err.Error())
	}
	if err != nil {
		return nil, err
	}
	return &DrainEndResponse{Body: Envelope[DrainEnd]{Data: DrainEnd{End: string(end)}}}, nil
}

// GetUpgradeRequest is the request of GetUpgrade.
type GetUpgradeRequest struct{}

// GetUpgradeResponse is the response of GetUpgrade.
type GetUpgradeResponse struct {
	Body Envelope[Upgrade]
}

// GetUpgrade returns the error of the last upgrade.
func (h *handlers) GetUpgrade(_ context.Context, _ GetUpgradeRequest) (*GetUpgradeResponse, error) {
	return &GetUpgradeResponse{Body: Envelope[Upgrade]{Data: Upgrade{Failure: h.engine.UpgradeFailure()}}}, nil
}

func drainOf(state engine.DrainState) Drain {
	return Drain{On: state.On, Waiting: int64(state.Waiting)}
}
