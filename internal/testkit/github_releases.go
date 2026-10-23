package testkit

import (
	"archive/tar"
	"compress/gzip"
	"net/http"
	"slices"
	"strconv"
	"strings"
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

// getLatestRelease needs no token, as on GitHub for a public repository, unless SetAnonymousRateLimited is on.
func (g *FakeGitHub) getLatestRelease(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.refuseAnonymous(w, r) {
		return
	}
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

// compare needs no token, as on GitHub for a public repository, unless SetAnonymousRateLimited is on. For a repository
// with a git repository it gives the status, behind_by, ahead_by, the commits and the files of the comparison in the
// path of r. For Mobius-Toolkit/Mobius it gives the commits of SetComparedCommits.
func (g *FakeGitHub) compare(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.refuseAnonymous(w, r) {
		return
	}
	if repository(r) != "Mobius-Toolkit/Mobius" {
		g.compareGit(w, r)
		return
	}
	commits := make([]commitJSON, len(g.comparedCommits))
	for i, message := range g.comparedCommits {
		commits[i].Commit.Message = message
	}
	writeJSON(w, http.StatusOK, map[string][]commitJSON{"commits": commits})
}

// compareGit gives the comparison in the path of r, read from the git repository of the repository. The caller must
// hold the lock.
func (g *FakeGitHub) compareGit(w http.ResponseWriter, r *http.Request) {
	if !slices.Contains(g.repositories, repository(r)) {
		notFound(w)
		return
	}
	remote := g.Remote(repository(r))
	base, head, _ := strings.Cut(r.PathValue("basehead"), "...")
	counts, err := gitCommand(remote, "rev-list", "--left-right", "--count", base+"..."+head).Output()
	if err != nil {
		notFound(w)
		return
	}
	behindText, aheadText, _ := strings.Cut(strings.TrimSpace(string(counts)), "\t")
	behind, behindErr := strconv.Atoi(behindText)
	ahead, aheadErr := strconv.Atoi(aheadText)
	if behindErr != nil || aheadErr != nil {
		g.t.Errorf("rev-list counts %q", counts)
	}
	status := "diverged"
	switch {
	case behind == 0 && ahead == 0:
		status = "identical"
	case behind == 0:
		status = "ahead"
	case ahead == 0:
		status = "behind"
	}
	log, err := gitCommand(remote, "log", "--reverse", "--topo-order", "--format=%H%x00%P%x00%B%x1e", base+".."+head).Output()
	if err != nil {
		notFound(w)
		return
	}
	commits := []map[string]any{}
	for record := range strings.SplitSeq(string(log), "\x1e") {
		sha, rest, _ := strings.Cut(strings.TrimSpace(record), "\x00")
		parentText, message, _ := strings.Cut(rest, "\x00")
		if sha == "" {
			continue
		}
		parents := []map[string]string{}
		for parent := range strings.FieldsSeq(parentText) {
			parents = append(parents, map[string]string{"sha": parent})
		}
		commits = append(commits, map[string]any{"sha": sha, "parents": parents, "commit": map[string]any{"message": strings.TrimSpace(message)}})
	}
	names, err := gitCommand(remote, "diff", "--name-only", base+"..."+head).Output()
	if err != nil {
		notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "behind_by": behind, "ahead_by": ahead, "commits": commits, "files": fileList(string(names))})
}
