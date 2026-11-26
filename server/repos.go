package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Server-side GitHub repository clones for white-box review.
//
// The server, not the agent, runs git: the token is read from the encrypted
// settings store and handed to git only through the child's environment as an
// http.extraHeader for the GitHub host. It never appears in the clone URL, argv,
// .git/config, the remote URL or an error message, and the agent's executor uid
// cannot read the server's process environment. Clones land in <data>/repos so
// the workspace file manager and the agent's file tools can read them.

const (
	settingGitHubToken = "github_token"
	reposDirName       = "repos"
	repoGitTimeout     = 10 * time.Minute
)

// gitHubBase is the only remote host clones may come from. Tests point it at a
// local git http-backend.
var gitHubBase = "https://github.com"

var (
	repoOwnerRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	repoNameRe  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	repoRefRe   = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
	// repoDirRe is the on-disk name <owner>__<repo>.
	repoDirRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,39}__[A-Za-z0-9._-]{1,100}$`)
)

// repoMu serialises clone, pull and delete so two operations never touch the
// same directory at once.
var repoMu sync.Mutex

type repoInfo struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Ref       string `json:"ref"`
	Commit    string `json:"commit"`
	Path      string `json:"path"` // workspace-relative, e.g. repos/owner__repo
	UpdatedAt string `json:"updated_at"`
}

// parseGitHubURL accepts https://github.com/<owner>/<repo>[.git][/] only and
// returns the owner, repo and the clean clone URL.
func parseGitHubURL(raw string) (owner, repo, cloneURL string, err error) {
	raw = strings.TrimSpace(raw)
	base, err := url.Parse(gitHubBase)
	if err != nil {
		return "", "", "", err
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != base.Scheme || !strings.EqualFold(u.Host, base.Host) {
		return "", "", "", fmt.Errorf("only %s/<owner>/<repo> URLs are accepted", gitHubBase)
	}
	if u.User != nil {
		return "", "", "", errors.New("the URL must not carry credentials; set the GitHub token in Settings")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", "", "", errors.New("the URL must not have a query or fragment")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 {
		return "", "", "", fmt.Errorf("expected %s/<owner>/<repo>", gitHubBase)
	}
	owner, repo = parts[0], strings.TrimSuffix(parts[1], ".git")
	if !repoOwnerRe.MatchString(owner) || !repoNameRe.MatchString(repo) || repo == "." || repo == ".." {
		return "", "", "", errors.New("invalid owner or repository name")
	}
	return owner, repo, gitHubBase + "/" + owner + "/" + repo + ".git", nil
}

func validRepoRef(ref string) bool {
	return ref == "" || (repoRefRe.MatchString(ref) && !strings.HasPrefix(ref, "-") &&
		!strings.Contains(ref, "..") && !strings.HasSuffix(ref, "/") && !strings.HasSuffix(ref, ".lock"))
}

// gitEnv is a minimal environment for git: no inherited secrets, no prompts, no
// user or system config. A non-empty token becomes an Authorization header for
// the GitHub host only.
func gitEnv(token string) []string {
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/bin:/usr/bin:/bin"
	}
	env := []string{
		"PATH=" + path,
		"HOME=" + os.TempDir(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"LC_ALL=C",
	}
	if token != "" {
		basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
		env = append(env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http."+gitHubBase+"/.extraheader",
			"GIT_CONFIG_VALUE_0=Authorization: Basic "+basic,
		)
	}
	return env
}

// runGit runs git with the minimal env and returns stdout. Errors carry git's
// stderr with the token redacted.
func runGit(ctx context.Context, token, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv(token)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		if ctx.Err() != nil {
			msg = "git timed out: " + msg
		}
		return "", errors.New(redactToken(msg, token))
	}
	return strings.TrimSpace(out.String()), nil
}

func redactToken(s, token string) string {
	if token == "" {
		return s
	}
	s = strings.ReplaceAll(s, token, "[redacted]")
	return strings.ReplaceAll(s, base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token)), "[redacted]")
}

// cloneRepo clones into <root>/<owner>__<repo> through a temporary directory, so
// a failed or partial clone never shows up in the listing.
func cloneRepo(ctx context.Context, root, rawURL, ref, token string) (repoInfo, error) {
	owner, repo, cloneURL, err := parseGitHubURL(rawURL)
	if err != nil {
		return repoInfo{}, err
	}
	if !validRepoRef(ref) {
		return repoInfo{}, errors.New("invalid branch or tag name")
	}
	name := owner + "__" + repo
	dest := filepath.Join(root, name)
	if _, err := os.Lstat(dest); err == nil {
		return repoInfo{}, fmt.Errorf("%s is already cloned; pull or delete it", name)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return repoInfo{}, err
	}
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	tmp := filepath.Join(root, ".tmp-"+name+"-"+hex.EncodeToString(rnd[:]))
	defer os.RemoveAll(tmp)
	args := []string{"clone", "--quiet", "--no-recurse-submodules"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, "--", cloneURL, tmp)
	if _, err := runGit(ctx, token, root, args...); err != nil {
		return repoInfo{}, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return repoInfo{}, err
	}
	return readRepoInfo(ctx, root, name)
}

// pullRepo fast-forwards the checked-out branch from origin.
func pullRepo(ctx context.Context, root, name, token string) (repoInfo, error) {
	dir, err := repoDir(root, name)
	if err != nil {
		return repoInfo{}, err
	}
	if _, err := runGit(ctx, token, dir, "-c", "safe.directory="+dir, "pull", "--quiet", "--ff-only", "--no-recurse-submodules"); err != nil {
		return repoInfo{}, err
	}
	return readRepoInfo(ctx, root, name)
}

func deleteRepo(root, name string) error {
	dir, err := repoDir(root, name)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// repoDir resolves an existing clone by its on-disk name.
func repoDir(root, name string) (string, error) {
	if !repoDirRe.MatchString(name) {
		return "", errors.New("invalid repository name")
	}
	dir := filepath.Join(root, name)
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() {
		return "", errors.New("repository not found")
	}
	return dir, nil
}

func readRepoInfo(ctx context.Context, root, name string) (repoInfo, error) {
	dir, err := repoDir(root, name)
	if err != nil {
		return repoInfo{}, err
	}
	git := func(args ...string) string {
		out, _ := runGit(ctx, "", dir, append([]string{"-c", "safe.directory=" + dir}, args...)...)
		return out
	}
	info := repoInfo{
		Name:   name,
		URL:    git("remote", "get-url", "origin"),
		Ref:    git("rev-parse", "--abbrev-ref", "HEAD"),
		Commit: git("rev-parse", "HEAD"),
		Path:   reposDirName + "/" + name,
	}
	if info.Ref == "HEAD" { // detached, e.g. cloned at a tag
		if tag := git("describe", "--tags", "--exact-match"); tag != "" {
			info.Ref = tag
		}
	}
	mod := filepath.Join(dir, ".git", "FETCH_HEAD")
	if _, err := os.Stat(mod); err != nil {
		mod = filepath.Join(dir, ".git", "HEAD")
	}
	if fi, err := os.Stat(mod); err == nil {
		info.UpdatedAt = fi.ModTime().UTC().Format(time.RFC3339)
	}
	return info, nil
}

func listRepos(ctx context.Context, root string) ([]repoInfo, error) {
	ents, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return []repoInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []repoInfo{}
	for _, e := range ents {
		if !e.IsDir() || !repoDirRe.MatchString(e.Name()) {
			continue
		}
		if info, err := readRepoInfo(ctx, root, e.Name()); err == nil {
			out = append(out, info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ---- HTTP handlers ----

func (s *Server) reposRoot() string { return filepath.Join(s.m.dir, reposDirName) }

func (s *Server) gitHubToken() string {
	v, _, _ := s.m.pg.GetSetting(settingGitHubToken)
	return strings.TrimSpace(v)
}

// GET /api/repos
func (s *Server) reposList(w http.ResponseWriter, r *http.Request) {
	repos, err := listRepos(r.Context(), s.reposRoot())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"repos": repos, "token_set": s.gitHubToken() != ""})
}

// POST /api/repos {url, ref?}
func (s *Server) reposClone(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
		Ref string `json:"ref"`
	}
	if err := decode(w, r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if _, _, _, err := parseGitHubURL(req.URL); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	repoMu.Lock()
	defer repoMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), repoGitTimeout)
	defer cancel()
	info, err := cloneRepo(ctx, s.reposRoot(), req.URL, strings.TrimSpace(req.Ref), s.gitHubToken())
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, info)
}

// POST /api/repos/{name}/pull
func (s *Server) reposPull(w http.ResponseWriter, r *http.Request) {
	repoMu.Lock()
	defer repoMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), repoGitTimeout)
	defer cancel()
	info, err := pullRepo(ctx, s.reposRoot(), r.PathValue("name"), s.gitHubToken())
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, info)
}

// DELETE /api/repos/{name}
func (s *Server) reposDelete(w http.ResponseWriter, r *http.Request) {
	repoMu.Lock()
	defer repoMu.Unlock()
	if err := deleteRepo(s.reposRoot(), r.PathValue("name")); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
