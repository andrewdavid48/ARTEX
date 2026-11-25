package server

import (
	"maps"
	"slices"
	"testing"
)

func TestStripProxyArgsLeavesBrowserMCPFlags(t *testing.T) {
	in := []string{"--headless", "--browser", "chromium", "--proxy-server", "http://127.0.0.1:8788"}
	got := stripProxyArgs(in)
	want := []string{"--headless", "--browser", "chromium"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if in[3] != "--proxy-server" {
		t.Fatal("input mutated")
	}

	equals := []string{"--headless", "--proxy-server=http://127.0.0.1:8788", "--browser", "chromium"}
	got = stripProxyArgs(equals)
	if !slices.Equal(got, want) {
		t.Fatalf("equals form: got %q, want %q", got, want)
	}
}

// Chromium does not read NODE_EXTRA_CA_CERTS: with the recording proxy attached
// the browser MCP must also be told to accept the certificates the proxy signs,
// and must lose that again when capture is turned off.
func TestBrowserMCPProxyConfigAcceptsCaptureCA(t *testing.T) {
	const proxy, cert = "http://127.0.0.1:8788", "/app/data/traffic/_ca/mitmproxy-ca-cert.pem"
	base := []string{"--headless", "--browser", "chromium", "--ignore-https-errors"}

	args, env := browserMCPProxyConfig(base, map[string]string{"KEEP": "1"}, proxy, cert)
	if want := append(slices.Clone(base), "--proxy-server", proxy); !slices.Equal(args, want) {
		t.Fatalf("capture on: args %q, want %q", args, want)
	}
	wantEnv := map[string]string{"KEEP": "1", "NODE_EXTRA_CA_CERTS": cert, browserMCPIgnoreHTTPSEnv: "true"}
	if !maps.Equal(env, wantEnv) {
		t.Fatalf("capture on: env %v, want %v", env, wantEnv)
	}

	// Capture off: the proxy and both trust variables go; the operator's own
	// --ignore-https-errors argument stays.
	args, env = browserMCPProxyConfig(args, env, "", "")
	if !slices.Equal(args, base) {
		t.Fatalf("capture off: args %q, want %q", args, base)
	}
	if want := map[string]string{"KEEP": "1"}; !maps.Equal(env, want) {
		t.Fatalf("capture off: env %v, want %v", env, want)
	}

	// A global proxy without capture does not re-sign anything: no CA, so the
	// browser keeps validating certificates.
	_, env = browserMCPProxyConfig(base, map[string]string{}, "http://proxy.test:3128", "")
	if len(env) != 0 {
		t.Fatalf("proxy without a CA: env %v, want empty", env)
	}
}
