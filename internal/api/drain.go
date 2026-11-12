package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
)

// Drain is the state of the drain before an upgrade.
type Drain struct {
	// On is true while the drain holds the new Workers. A completed drain stays on until a cancel or the restart
	On bool `gork:"on"`
	// Waiting is the number of agents and npm upgrades that the drain waits for
	Waiting int64 `gork:"waiting"`
}

// DrainEnd tells how a drain ended.
type DrainEnd struct {
	// End is drained when no agent and no npm upgrade of Mobius runs, and cancelled when the Owner cancelled the drain
	End string `gork:"end" validate:"oneof=drained cancelled"`
}

// Upgrade is the state of the upgrade.
type Upgrade struct {
	// Step is idle when no upgrade runs, and else the step of the upgrade: release, download, retry (the wait before the
	// next try), drain or restart
	Step string `gork:"step" validate:"oneof=idle release download retry drain restart"`
	// Tries is the number of failed tries since the start of the upgrade
	Tries int64 `gork:"tries"`
	// Failure is the text of the last error. It is empty when the upgrade has no error
	Failure string `gork:"failure"`
	// NextTry is the time of the next try in the step retry, or null
	NextTry *time.Time `gork:"nextTry"`
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

// DrainEndResponse is the response of StartDrain.
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

// UpgradeResponse is the response of StartUpgrade and of GetUpgrade.
type UpgradeResponse struct {
	Body Envelope[Upgrade]
}

// StartUpgrade starts the upgrade in the background and returns its state. The upgrade reads the newest release,
// downloads it, drains the agents, and restarts Mobius with the new release. It tries again after an error that can
// pass, until it is done or the Owner cancels it.
func (h *handlers) StartUpgrade(_ context.Context, _ StartUpgradeRequest) (*UpgradeResponse, error) {
	state, err := h.engine.Upgrade()
	if engine.Refused(err) {
		return nil, api.NewHTTPError(http.StatusConflict, err.Error())
	}
	if err != nil {
		return nil, err
	}
	return &UpgradeResponse{Body: Envelope[Upgrade]{Data: upgradeOf(state)}}, nil
}

// CancelUpgradeRequest is the request of CancelUpgrade.
type CancelUpgradeRequest struct{}

// CancelUpgrade ends the upgrade and the drain. It does nothing while the upgrade replaces the program. With no upgrade,
// it ends only the drain.
func (h *handlers) CancelUpgrade(_ context.Context, _ CancelUpgradeRequest) error {
	h.engine.CancelUpgrade()
	return nil
}

// GetUpgradeRequest is the request of GetUpgrade.
type GetUpgradeRequest struct{}

// GetUpgrade returns the state of the upgrade.
func (h *handlers) GetUpgrade(_ context.Context, _ GetUpgradeRequest) (*UpgradeResponse, error) {
	return &UpgradeResponse{Body: Envelope[Upgrade]{Data: upgradeOf(h.engine.UpgradeStatus())}}, nil
}

func upgradeOf(state engine.UpgradeState) Upgrade {
	upgrade := Upgrade{Step: string(state.Step), Tries: int64(state.Tries), Failure: state.Failure}
	if !state.NextTry.IsZero() {
		upgrade.NextTry = &state.NextTry
	}
	return upgrade
}

func drainOf(state engine.DrainState) Drain {
	return Drain{On: state.On, Waiting: int64(state.Waiting)}
}

// Release is the newest release of Mobius.
type Release struct {
	// Version is the tag of the newest release when it is newer than this program, for example v0.2.0. It is empty
	// when no newer release exists, and for a local build
	Version string `gork:"version"`
}

// GetReleaseRequest is the request of GetRelease.
type GetReleaseRequest struct{}

// GetReleaseResponse is the response of GetRelease.
type GetReleaseResponse struct {
	Body Envelope[Release]
}

// GetRelease returns the newest release of Mobius when it is newer than this program.
func (h *handlers) GetRelease(ctx context.Context, _ GetReleaseRequest) (*GetReleaseResponse, error) {
	version, err := h.engine.NewRelease(ctx)
	if err != nil {
		return nil, err
	}
	return &GetReleaseResponse{Body: Envelope[Release]{Data: Release{Version: version}}}, nil
}

// Version is the version of the program.
type Version struct {
	// Release is the release tag of this program, for example v0.2.0. It is empty for a local build
	Release string `gork:"release"`
}

// GetVersionRequest is the request of GetVersion.
type GetVersionRequest struct{}

// GetVersionResponse is the response of GetVersion.
type GetVersionResponse struct {
	Body Envelope[Version]
}

// GetVersion returns the release tag of this program.
func (h *handlers) GetVersion(_ context.Context, _ GetVersionRequest) (*GetVersionResponse, error) {
	return &GetVersionResponse{Body: Envelope[Version]{Data: Version{Release: engine.Release}}}, nil
}

// ListReleaseChangesRequest is the request of ListReleaseChanges.
type ListReleaseChangesRequest struct{}

// ListReleaseChangesResponse is the response of ListReleaseChanges.
type ListReleaseChangesResponse struct {
	Body Envelope[[]string]
}

// ListReleaseChanges returns the first line of the message of each commit after this release up to the newest
// release of Mobius, the newest first.
func (h *handlers) ListReleaseChanges(ctx context.Context, _ ListReleaseChangesRequest) (*ListReleaseChangesResponse, error) {
	changes, err := h.engine.ReleaseChanges(ctx)
	if engine.Refused(err) {
		return nil, api.NewHTTPError(http.StatusConflict, err.Error())
	}
	if err != nil {
		return nil, err
	}
	return &ListReleaseChangesResponse{Body: Envelope[[]string]{Data: changes}}, nil
}
