package testkit

import "net/http"

type assetJSON struct {
	Name string `json:"name"`
}

type releaseJSON struct {
	TagName string      `json:"tag_name"`
	Assets  []assetJSON `json:"assets"`
}

// SetLatestRelease makes tag with the files assets the latest release of Mobius-Toolkit/Mobius. With no
// SetLatestRelease, Mobius-Toolkit/Mobius has no release.
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
