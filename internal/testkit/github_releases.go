package testkit

import (
	"archive/tar"
	"compress/gzip"
	"net/http"
	"slices"
)

type assetJSON struct {
	Name string `json:"name"`
}

type releaseJSON struct {
	TagName string      `json:"tag_name"`
	Assets  []assetJSON `json:"assets"`
}

// SetLatestRelease makes tag with the files assets the latest release of Mobius-Toolkit/Mobius. Each file is a
// tar.gz archive with the program mobius, and the program is the text "<tag>/<file name>". With no SetLatestRelease,
// Mobius-Toolkit/Mobius has no release.
func (g *FakeGitHub) SetLatestRelease(tag string, assets ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.latestRelease = &releaseJSON{TagName: tag, Assets: []assetJSON{}}
	for _, asset := range assets {
		g.latestRelease.Assets = append(g.latestRelease.Assets, assetJSON{asset})
	}
}

// getLatestRelease needs no token, as on GitHub for a public repository.
func (g *FakeGitHub) getLatestRelease(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if repository(r) != "Mobius-Toolkit/Mobius" || g.latestRelease == nil {
		notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, g.latestRelease)
}

// downloadReleaseFile needs no token, as on GitHub for a public repository.
func (g *FakeGitHub) downloadReleaseFile(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	tag, name := r.PathValue("tag"), r.PathValue("name")
	if repository(r) != "Mobius-Toolkit/Mobius" || g.latestRelease == nil || g.latestRelease.TagName != tag ||
		!slices.Contains(g.latestRelease.Assets, assetJSON{name}) {
		notFound(w)
		return
	}
	program := []byte(tag + "/" + name)
	zipped := gzip.NewWriter(w)
	archive := tar.NewWriter(zipped)
	_ = archive.WriteHeader(&tar.Header{Name: "mobius", Mode: 0o755, Size: int64(len(program))})
	_, _ = archive.Write(program)
	_ = archive.Close()
	_ = zipped.Close()
}

type commitJSON struct {
	Commit struct {
		Message string `json:"message"`
	} `json:"commit"`
}

// SetComparedCommits makes each comparison of two commits of Mobius-Toolkit/Mobius give the commits with messages,
// the oldest first.
func (g *FakeGitHub) SetComparedCommits(messages ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.comparedCommits = messages
}

// compare needs no token, as on GitHub for a public repository.
func (g *FakeGitHub) compare(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if repository(r) != "Mobius-Toolkit/Mobius" {
		notFound(w)
		return
	}
	commits := make([]commitJSON, len(g.comparedCommits))
	for i, message := range g.comparedCommits {
		commits[i].Commit.Message = message
	}
	writeJSON(w, http.StatusOK, map[string][]commitJSON{"commits": commits})
}
