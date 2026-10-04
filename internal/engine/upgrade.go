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
)

// Release is the release tag of this program, for example v0.2.0. The release workflow sets it with
// -ldflags "-X github.com/Mobius-Toolkit/mobius-go/internal/engine.Release=<tag>". A local build has no release tag.
var Release string

// execDelay gives the web UI the response of the upgrade call before the new program replaces this process.
const execDelay = time.Second

// Upgrade downloads the newest release, drains the agents, replaces the program and runs the new program in this
// process. It gives Drained when the new program starts, and Cancelled when the Owner cancels the drain.
// The drain has no time limit, so the upgrade goes on after the end of ctx, and UpgradeFailure gives its error.
func (e *Engine) Upgrade(ctx context.Context) (DrainEnd, error) {
	if !e.upgrading.CompareAndSwap(false, true) {
		return "", refuse("An upgrade runs now.")
	}
	e.setUpgradeFailure("")
	type result struct {
		end DrainEnd
		err error
	}
	done := make(chan result, 1)
	go func() {
		end, err := e.upgrade(context.WithoutCancel(ctx))
		if err != nil {
			e.setUpgradeFailure(err.Error())
		}
		e.upgrading.Store(false)
		done <- result{end, err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-done:
		return r.end, r.err
	}
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

// UpgradeFailure gives the error of the last upgrade, or "" when the last upgrade has no error.
func (e *Engine) UpgradeFailure() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.upgradeFailure
}

func (e *Engine) setUpgradeFailure(failure string) {
	e.mu.Lock()
	e.upgradeFailure = failure
	e.mu.Unlock()
	e.publish(Change{Upgrade: &failure})
}

func (e *Engine) upgrade(ctx context.Context) (DrainEnd, error) {
	if Release == "" {
		return "", refuse("This Mobius build is not a release.")
	}
	release, err := e.github.LatestRelease(ctx)
	if err != nil {
		return "", err
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
	if !slices.ContainsFunc(release.Assets, func(found *gh.ReleaseAsset) bool { return found.GetName() == asset }) {
		return "", refuse("Release %s has no %s.", tag, asset)
	}
	// After the swap, os.Executable can name the old file, so the path is fixed here.
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	staging, err := download(ctx, e.github.ReleaseURL(tag, asset), exe)
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	// A session can start between the end of the drain and the seal, so the drain repeats.
	for sealed := false; !sealed; {
		end, err := e.Drain(ctx)
		if err != nil || end == Cancelled {
			return end, err
		}
		switch e.seal() {
		case Drained:
			sealed = true
		case Cancelled:
			return Cancelled, nil
		}
	}
	if err := swap(staging, exe); err != nil {
		e.abortDrain()
		return "", err
	}
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

// download gets the release archive at url and extracts it into a new directory next to the program exe. The
// directory is on the file system of the program, so the renames of the swap stay on one file system.
func download(ctx context.Context, url, exe string) (string, error) {
	staging, err := os.MkdirTemp(filepath.Dir(exe), ".mobius-upgrade-")
	if err != nil {
		return "", err
	}
	archive := filepath.Join(staging, "mobius.tar.gz")
	curl := exec.CommandContext(ctx, "curl")
	curl.Args = append(curl.Args, "-fsSL", url, "-o", archive)
	tar := exec.CommandContext(ctx, "tar")
	tar.Args = append(tar.Args, "-xzf", archive)
	tar.Dir = staging
	for _, cmd := range []*exec.Cmd{curl, tar} {
		if err := run(cmd); err != nil {
			return "", errors.Join(err, os.RemoveAll(staging))
		}
	}
	if info, err := os.Stat(filepath.Join(staging, "mobius")); err != nil || !info.Mode().IsRegular() {
		return "", errors.Join(refuse("The archive at %s has no mobius program.", url), os.RemoveAll(staging))
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
