package server

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestParseGitHubURL(t *testing.T) {
	ok := map[string]string{
		"https://github.com/acme/web":      "https://github.com/acme/web.git",
		"https://github.com/acme/web.git":  "https://github.com/acme/web.git",
		"https://github.com/acme/web/":     "https://github.com/acme/web.git",
		" https://GitHub.com/a-b/c.d_e-f ": "https://github.com/a-b/c.d_e-f.git",
	}
	for in, want := range ok {
		_, _, got, err := parseGitHubURL(in)
		if err != nil || got != want {
			t.Errorf("parseGitHubURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"http://github.com/acme/web",
		"ssh://git@github.com/acme/web.git",
		"git@github.com:acme/web.git",
		"file:///etc/passwd",
		"https://gitlab.com/acme/web",
		"https://github.com.evil.example/acme/web",
		"https://user:tok@github.com/acme/web",
		"https://tok@github.com/acme/web",
		"https://github.com/acme",
		"https://github.com/acme/web/tree/main",
		"https://github.com/../web",
		"https://github.com/acme/..",
		"https://github.com/acme/web?x=1",
		"https://github.com/-acme/web",
		"",
	}
	for _, in := range bad {
		if _, _, _, err := parseGitHubURL(in); err == nil {
			t.Errorf("parseGitHubURL(%q) accepted", in)
		}
	}
}

func TestValidRepoRef(t *testing.T) {
	for _, r := range []string{"", "main", "release/v2", "v1.2.3"} {
		if !validRepoRef(r) {
			t.Errorf("ref %q rejected", r)
		}
	}
	for _, r := range []string{"-x", "--upload-pack=sh", "a..b", "a b", "a/", "x.lock", "a;b"} {
		if validRepoRef(r) {
			t.Errorf("ref %q accepted", r)
		}
	}
}

func TestRepoDirRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"..", "../x", "a/b", ".tmp-x__y", "x", ""} {
		if _, err := repoDir(root, n); err == nil {
			t.Errorf("repoDir(%q) accepted", n)
		}
		if err := deleteRepo(root, n); err == nil {
			t.Errorf("deleteRepo(%q) accepted", n)
		}
	}
}

// fakeGitHub serves a bare repository through git http-backend and records the
// Authorization headers it receives.
func fakeGitHub(t *testing.T) (srcRepo string, auths func() []string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	execPath, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Skip("git --exec-path failed")
	}
	backend := filepath.Join(strings.TrimSpace(string(execPath)), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skip("git-http-backend not available")
	}
	projects := t.TempDir()
	src := filepath.Join(t.TempDir(), "src")
	run := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(gitEnv(""), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	run(src, "init", "--quiet", "-b", "main")
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(src, "add", ".")
	run(src, "commit", "--quiet", "-m", "one")
	bare := filepath.Join(projects, "acme", "web.git")
	run(projects, "clone", "--quiet", "--bare", src, bare)
	run(src, "remote", "add", "origin", bare)

	var mu sync.Mutex
	var seen []string
	h := &cgi.Handler{Path: backend, Env: []string{"GIT_PROJECT_ROOT=" + projects, "GIT_HTTP_EXPORT_ALL=1"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	old := gitHubBase
	gitHubBase = srv.URL
	t.Cleanup(func() { gitHubBase = old })
	return src, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), seen...) }
}

func TestCloneRepoKeepsTokenOutOfTheClone(t *testing.T) {
	src, auths := fakeGitHub(t)
	root := filepath.Join(t.TempDir(), "repos")
	const token = "ghp_secret_test_token_123"
	ctx := context.Background()

	info, err := cloneRepo(ctx, root, gitHubBase+"/acme/web", "", token)
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	if info.Name != "acme__web" || info.Ref != "main" || len(info.Commit) != 40 || info.Path != "repos/acme__web" {
		t.Fatalf("unexpected info %+v", info)
	}
	if info.URL != gitHubBase+"/acme/web.git" {
		t.Fatalf("remote URL %q", info.URL)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token))
	sent := false
	for _, a := range auths() {
		sent = sent || a == wantAuth
	}
	if !sent {
		t.Fatalf("token header never sent; saw %q", auths())
	}
	// Nothing on disk may carry the token, in clear or base64.
	b64 := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		b, _ := os.ReadFile(p)
		if strings.Contains(string(b), token) || strings.Contains(string(b), b64) {
			t.Errorf("token found in %s", p)
		}
		return nil
	})
	if _, err := os.Stat(filepath.Join(root, "acme__web", "a.txt")); err != nil {
		t.Fatal("checkout missing")
	}

	if _, err := cloneRepo(ctx, root, gitHubBase+"/acme/web", "", token); err == nil {
		t.Fatal("second clone of the same repo accepted")
	}

	// A new upstream commit arrives through pull.
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"commit", "--quiet", "-am", "two"}, {"push", "--quiet", "origin", "main"}} {
		c := exec.Command("git", args...)
		c.Dir = src
		c.Env = append(gitEnv(""), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	pulled, err := pullRepo(ctx, root, "acme__web", token)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if pulled.Commit == info.Commit {
		t.Fatal("pull did not advance HEAD")
	}

	list, err := listRepos(ctx, root)
	if err != nil || len(list) != 1 || list[0].Name != "acme__web" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := deleteRepo(root, "acme__web"); err != nil {
		t.Fatal(err)
	}
	if list, _ := listRepos(ctx, root); len(list) != 0 {
		t.Fatalf("repo still listed after delete: %+v", list)
	}
}

func TestCloneRepoFailureLeavesNothingAndRedactsToken(t *testing.T) {
	fakeGitHub(t)
	root := filepath.Join(t.TempDir(), "repos")
	const token = "ghp_secret_test_token_456"
	_, err := cloneRepo(context.Background(), root, gitHubBase+"/acme/missing", "", token)
	if err == nil {
		t.Fatal("clone of a missing repo succeeded")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error leaks the token: %v", err)
	}
	ents, _ := os.ReadDir(root)
	if len(ents) != 0 {
		t.Fatalf("failed clone left %d entries behind", len(ents))
	}
}

func TestRedactToken(t *testing.T) {
	tok := "abc123"
	b64 := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + tok))
	got := redactToken("x abc123 y "+b64, tok)
	if strings.Contains(got, tok) || strings.Contains(got, b64) {
		t.Fatalf("not redacted: %q", got)
	}
}
