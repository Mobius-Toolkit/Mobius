package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	gh "github.com/google/go-github/v92/github"
	"golang.org/x/sys/unix"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
)

// Release is the release tag of this program, for example v0.2.0. The release workflow sets it with
// -ldflags "-X github.com/Mobius-Toolkit/Mobius/internal/engine.Release=<tag>". A local build has no release tag.
var Release string

// executable gives the path of this program.
var executable = os.Executable

// execDelay gives the web UI the response of the upgrade call before the new program replaces this process.
const execDelay = time.Second

// UpgradeStep is the step of an upgrade.
type UpgradeStep string

const (
	// UpgradeIdle tells that no upgrade runs.
	UpgradeIdle UpgradeStep = "idle"
	// UpgradeRelease is the read and the check of the newest release.
	UpgradeRelease UpgradeStep = "release"
	// UpgradeDownload is the download of the archive of the release.
	UpgradeDownload UpgradeStep = "download"
	// UpgradeRetry is the wait before the next try.
	UpgradeRetry UpgradeStep = "retry"
	// UpgradeDrain is the drain of the agents.
	UpgradeDrain UpgradeStep = "drain"
	// UpgradeRestart is the seal, the swap and the start of the new program.
	UpgradeRestart UpgradeStep = "restart"
)

const (
	// upgradeFirstWait is the wait after the first failed try. The wait doubles after each failed try.
	upgradeFirstWait = 10 * time.Second
	// upgradeMaxWait is the longest wait before a try.
	upgradeMaxWait = 5 * time.Minute
	// upgradeWaitPoll is the time between two reads of the clock in the wait before a try.
	upgradeWaitPoll = 100 * time.Millisecond
)

// UpgradeState is the state of the upgrade.
type UpgradeState struct {
	Step UpgradeStep
	// Tries is the number of failed tries since the start of the upgrade.
	Tries int
	// Failure is the text of the last error, or "".
	Failure string
	// NextTry is the time of the next try in the step UpgradeRetry, else the zero time.
	NextTry time.Time
}

// retryable marks an error that can pass, so the upgrade tries again after a wait.
type retryable struct{ error }

func (r retryable) Unwrap() error { return r.error }

// Upgrade starts an upgrade in the background and gives its state. The upgrade reads the newest release, downloads it,
// drains the agents, replaces the program and runs the new program in this process. It tries again after an error that
// can pass, until the upgrade is done or CancelUpgrade ends it. UpgradeStatus gives the state of the upgrade.
func (e *Engine) Upgrade() (UpgradeState, error) {
	e.upgradeMu.Lock()
	defer e.upgradeMu.Unlock()
	if e.upgradeState.Step != UpgradeIdle {
		return UpgradeState{}, refuse("An upgrade runs now.")
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.upgradeCancel = cancel
	e.upgradeDone = make(chan struct{})
	e.upgradeState = UpgradeState{Step: UpgradeRelease}
	state := e.upgradeState
	e.publish(Change{Upgrade: &state})
	go e.runUpgrade(ctx, e.upgradeDone)
	return state, nil
}

// CancelUpgrade ends the upgrade and the drain, and waits until the upgrade has ended. With no upgrade, it ends only
// the drain. It does nothing while the upgrade replaces the program.
func (e *Engine) CancelUpgrade() {
	e.upgradeMu.Lock()
	step := e.upgradeState.Step
	if step == UpgradeRestart {
		e.upgradeMu.Unlock()
		return
	}
	var done chan struct{}
	if step != UpgradeIdle {
		e.upgradeCancel()
		done = e.upgradeDone
	}
	e.upgradeMu.Unlock()
	e.CancelDrain()
	if done != nil {
		<-done
	}
}

// UpgradeStatus gives the state of the upgrade.
func (e *Engine) UpgradeStatus() UpgradeState {
	e.upgradeMu.Lock()
	defer e.upgradeMu.Unlock()
	return e.upgradeState
}

// changeUpgrade changes the state of the upgrade with change, and publishes the new state.
func (e *Engine) changeUpgrade(change func(*UpgradeState)) {
	e.upgradeMu.Lock()
	defer e.upgradeMu.Unlock()
	change(&e.upgradeState)
	state := e.upgradeState
	e.publish(Change{Upgrade: &state})
}

func (e *Engine) setUpgradeStep(step UpgradeStep) {
	e.changeUpgrade(func(state *UpgradeState) { state.Step, state.NextTry = step, time.Time{} })
}

// finishUpgrade moves the upgrade to the step UpgradeIdle with the last error failure.
func (e *Engine) finishUpgrade(failure string) {
	e.changeUpgrade(func(state *UpgradeState) {
		state.Step, state.Failure, state.NextTry = UpgradeIdle, failure, time.Time{}
	})
}

// beginRestart moves the upgrade to the step UpgradeRestart, where CancelUpgrade does nothing. It gives false when
// CancelUpgrade ended ctx.
func (e *Engine) beginRestart(ctx context.Context) bool {
	e.upgradeMu.Lock()
	defer e.upgradeMu.Unlock()
	if ctx.Err() != nil {
		return false
	}
	e.upgradeState.Step = UpgradeRestart
	state := e.upgradeState
	e.publish(Change{Upgrade: &state})
	return true
}

// runUpgrade runs the upgrade of ctx until it is done, it fails with no retry, or CancelUpgrade ends ctx.
func (e *Engine) runUpgrade(ctx context.Context, done chan struct{}) {
	defer close(done)
	wait, tries := upgradeFirstWait, 0
	for {
		end, err := e.upgrade(ctx)
		var again retryable
		switch {
		case err == nil && end == Drained:
			return
		case ctx.Err() != nil || err == nil:
			e.finishUpgrade("")
			return
		case !errors.As(err, &again):
			e.finishUpgrade(err.Error())
			return
		}
		tries++
		next := e.timeNow().Add(wait)
		e.changeUpgrade(func(state *UpgradeState) {
			state.Step, state.Tries, state.Failure, state.NextTry = UpgradeRetry, tries, err.Error(), next
		})
		if !e.waitUntil(ctx, next) {
			e.finishUpgrade("")
			return
		}
		wait = min(2*wait, upgradeMaxWait)
		e.setUpgradeStep(UpgradeRelease)
	}
}

// waitUntil waits until e.timeNow reaches at. It gives false when ctx ends first.
func (e *Engine) waitUntil(ctx context.Context, at time.Time) bool {
	poll := time.NewTicker(upgradeWaitPoll)
	defer poll.Stop()
	for e.timeNow().Before(at) {
		select {
		case <-ctx.Done():
			return false
		case <-poll.C:
		}
	}
	return true
}

// NewRelease gives the tag of the newest release of Mobius when it is newer than this program. It gives "" when no
// newer release exists, and for a local build.
func (e *Engine) NewRelease(ctx context.Context) (string, error) {
	if Release == "" {
		return "", nil
	}
	release, err := e.github.LatestRelease(ctx)
	if err != nil {
		return "", err
	}
	if !newerRelease(Release, release.GetTagName()) {
		return "", nil
	}
	return release.GetTagName(), nil
}

// ReleaseChanges gives the first line of the message of each commit after this release up to the newest release of
// Mobius, the newest first.
func (e *Engine) ReleaseChanges(ctx context.Context) ([]string, error) {
	if Release == "" {
		return nil, refuse("This Mobius build is not a release.")
	}
	release, err := e.github.LatestRelease(ctx)
	if err != nil {
		return nil, err
	}
	messages, err := e.github.CommitMessages(ctx, Release, release.GetTagName())
	if err != nil {
		return nil, err
	}
	changes := make([]string, 0, len(messages))
	for _, message := range slices.Backward(messages) {
		title, _, _ := strings.Cut(message, "\n")
		changes = append(changes, title)
	}
	return changes, nil
}

func (e *Engine) upgrade(ctx context.Context) (DrainEnd, error) {
	if Release == "" {
		return "", refuse("This Mobius build is not a release.")
	}
	release, err := e.github.LatestRelease(ctx)
	if err != nil {
		return "", retryable{err}
	}
	tag := release.GetTagName()
	if !newerRelease(Release, tag) {
		return "", refuse("%s is the newest release.", tag)
	}
	target, err := releaseTarget(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	asset := "mobius-" + target + ".tar.gz"
	index := slices.IndexFunc(release.Assets, func(found *gh.ReleaseAsset) bool { return found.GetName() == asset })
	if index < 0 {
		return "", retryable{refuse("Release %s has no %s.", tag, asset)}
	}
	// After the swap, os.Executable can name the old file, so the path is fixed here.
	exe, err := executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	e.setUpgradeStep(UpgradeDownload)
	staging, err := download(ctx, e.github, tag, release.Assets[index], exe)
	if err != nil {
		return "", retryable{err}
	}
	defer func() { _ = os.RemoveAll(staging) }()
	e.setUpgradeStep(UpgradeDrain)
	// A session can start between the end of the drain and the seal, so the drain repeats.
	for sealed := false; !sealed; {
		end, err := e.Drain(ctx)
		if err != nil || end == Cancelled {
			return end, err
		}
		if !e.beginRestart(ctx) {
			return Cancelled, nil
		}
		switch e.seal() {
		case Drained:
			sealed = true
		case Cancelled:
			return Cancelled, nil
		default:
			e.setUpgradeStep(UpgradeDrain)
		}
	}
	if err := swap(staging, exe); err != nil {
		e.abortDrain()
		return "", err
	}
	e.closePaused()
	e.closeKeptSessions()
	// The exec keeps the environment, the working directory and the terminal. A child process goes on after the
	// exec, and the new program does not know it, so the drain must be complete.
	time.AfterFunc(execDelay, func() {
		err := unix.Exec(exe, append([]string{exe}, os.Args[1:]...), os.Environ())
		log.Printf("the restart after the upgrade failed: %v", err)
		os.Exit(1)
	})
	return Drained, nil
}

// newerRelease tells if the release tag latest is newer than the release tag current. A tag is vX.Y.Z.
func newerRelease(current, latest string) bool {
	currentVersion, ok := releaseVersion(current)
	latestVersion, latestOK := releaseVersion(latest)
	return ok && latestOK && slices.Compare(latestVersion, currentVersion) > 0
}

func releaseVersion(tag string) ([]uint64, bool) {
	rest, ok := strings.CutPrefix(tag, "v")
	parts := strings.Split(rest, ".")
	if !ok || len(parts) != 3 {
		return nil, false
	}
	version := make([]uint64, 0, len(parts))
	for _, part := range parts {
		number, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return nil, false
		}
		version = append(version, number)
	}
	return version, true
}

// releaseTarget gives the target name of the release files of the operating system goos on the architecture goarch.
func releaseTarget(goos, goarch string) (string, error) {
	switch {
	case goos == "darwin" && goarch == "arm64":
		return "aarch64-apple-darwin", nil
	case goos == "linux" && goarch == "amd64":
		return "x86_64-unknown-linux-gnu", nil
	}
	return "", refuse("Mobius has no release for %s %s.", goarch, goos)
}

// download gets the release archive of asset and extracts it into a new directory next to the program exe. The
// directory is on the file system of the program, so the renames of the swap stay on one file system.
func download(ctx context.Context, source *github.GitHub, tag string, asset *gh.ReleaseAsset, exe string) (string, error) {
	staging, err := os.MkdirTemp(filepath.Dir(exe), ".mobius-upgrade-")
	if err != nil {
		return "", err
	}
	archive := filepath.Join(staging, "mobius.tar.gz")
	if err := source.DownloadReleaseAsset(ctx, asset.GetID(), archive); err != nil {
		return "", errors.Join(fmt.Errorf("download of %s of release %s failed: %w", asset.GetName(), tag, err), os.RemoveAll(staging))
	}
	tar := exec.CommandContext(ctx, "tar")
	tar.Args = append(tar.Args, "-xzf", archive)
	tar.Dir = staging
	if err := run(tar); err != nil {
		return "", errors.Join(err, os.RemoveAll(staging))
	}
	if info, err := os.Stat(filepath.Join(staging, "mobius")); err != nil || !info.Mode().IsRegular() {
		return "", errors.Join(refuse("The archive %s of release %s has no mobius program.", asset.GetName(), tag), os.RemoveAll(staging))
	}
	return staging, nil
}

func run(cmd *exec.Cmd) error {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed: %w: %s", cmd.Args[0], err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// swap moves the program exe aside and moves the new program of staging to its place. A failed swap moves the
// program back.
func swap(staging, exe string) error {
	previous := filepath.Join(staging, "previous")
	if err := os.Rename(exe, previous); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(staging, "mobius"), exe); err != nil {
		return errors.Join(err, os.Rename(previous, exe))
	}
	return nil
}
