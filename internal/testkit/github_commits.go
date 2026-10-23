package testkit

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// listCommits gives the commits of the branch sha of the git repository of the repository, the newest first, with a
// commit date at or after since.
func (g *FakeGitHub) listCommits(w http.ResponseWriter, r *http.Request) {
	args := []string{"log", "--format=%H%x00%cI%x00%s", r.URL.Query().Get("sha")}
	if since := r.URL.Query().Get("since"); since != "" {
		args = append(args, "--since="+since)
	}
	out, err := gitCommand(g.Remote(repository(r)), args...).Output()
	if err != nil {
		notFound(w)
		return
	}
	commits := []map[string]any{}
	for line := range strings.Lines(strings.TrimSpace(string(out))) {
		sha, rest, _ := strings.Cut(strings.TrimSpace(line), "\x00")
		date, subject, _ := strings.Cut(rest, "\x00")
		commits = append(commits, map[string]any{"sha": sha, "commit": map[string]any{"message": subject, "committer": map[string]string{"date": date}}})
	}
	writeJSON(w, http.StatusOK, page(w, r, commits))
}

// getCommit gives the files that the commit sha changes.
func (g *FakeGitHub) getCommit(w http.ResponseWriter, r *http.Request) {
	out, err := gitCommand(g.Remote(repository(r)), "show", "--name-only", "--format=", r.PathValue("sha")).Output()
	if err != nil {
		notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sha": r.PathValue("sha"), "files": fileList(string(out))})
}

// pullRequestFiles gives the files that the head of the pull request changes since its merge base with its base.
func (g *FakeGitHub) pullRequestFiles(w http.ResponseWriter, r *http.Request) {
	number, _ := strconv.ParseInt(r.PathValue("number"), 10, 64)
	g.mu.Lock()
	index := slices.IndexFunc(g.pullRequests, func(pull pullRequest) bool { return pull.repository == repository(r) && pull.Number == number })
	if index < 0 {
		g.mu.Unlock()
		notFound(w)
		return
	}
	pull := g.pullRequests[index]
	g.mu.Unlock()
	out, err := gitCommand(g.Remote(repository(r)), "diff", "--name-only", pull.Base+"..."+pull.Head).Output()
	if err != nil {
		notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, page(w, r, fileList(string(out))))
}

func fileList(names string) []map[string]string {
	files := []map[string]string{}
	for name := range strings.Lines(names) {
		files = append(files, map[string]string{"filename": strings.TrimSpace(name)})
	}
	return files
}
