package traffic

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"crypto/rand"
	"encoding/hex"
	"io"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	mproxy "github.com/lqqyt2423/go-mitmproxy/proxy"
)

func withRespEncoding(coding string) flowOpt {
	return func(f *mproxy.Flow) { f.Response.Header.Set("Content-Encoding", coding) }
}

// compress encodes plain the way a server would for the named test case.
func compress(t *testing.T, kind string, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	var w io.WriteCloser
	var err error
	switch kind {
	case "gzip":
		w = gzip.NewWriter(&buf)
	case "deflate-zlib":
		w = zlib.NewWriter(&buf)
	case "deflate-raw":
		w, err = flate.NewWriter(&buf, flate.DefaultCompression)
	case "br":
		w = brotli.NewWriter(&buf)
	case "zstd":
		w, err = zstd.NewWriter(&buf)
	default:
		t.Fatalf("unknown kind %q", kind)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A browser asks for compressed responses; Get must show what the target said,
// not the compressed bytes, while the stored body stays as it crossed the wire.
func TestGetDecodesContentEncoding(t *testing.T) {
	const plain = `{"user":"alice","role":"admin","token":"decode-me-7f3a"}`
	for _, tc := range []struct{ kind, header string }{
		{"gzip", "gzip"},
		{"gzip", "x-gzip"},
		{"deflate-zlib", "deflate"},
		{"deflate-raw", "deflate"},
		{"br", "br"},
		{"zstd", "zstd"},
		{"br", "identity, BR"},
	} {
		t.Run(tc.kind+"/"+tc.header, func(t *testing.T) {
			tr, _ := openTraffic(t)
			wire := compress(t, tc.kind, []byte(plain))
			tr.record(newFlow("api.example.com", "GET", "/me", nil, wire, withRespEncoding(tc.header)))

			_, resp, err := tr.Get(onlyExchangeID(t, tr))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(resp, "\n"+plain) {
				t.Fatalf("response body not decoded:\n%q", resp)
			}
			if !strings.Contains(resp, "Content-Encoding: "+tc.header) {
				t.Fatalf("recorded headers changed:\n%q", resp)
			}
			var stored []byte
			if err := tr.DB().QueryRow(`SELECT resp_body FROM exchange_bodies`).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(stored, wire) {
				t.Fatal("stored body is no longer the wire bytes")
			}
		})
	}
}

// A body that does not decode, a coding that is not handled and a stacked
// coding are shown as stored: never an error, never an empty body.
func TestGetKeepsUndecodableBodyAsStored(t *testing.T) {
	gz := compress(t, "gzip", []byte("hello"))
	for name, tc := range map[string]struct {
		header string
		body   []byte
	}{
		"corrupt":     {"br", []byte("this is not brotli")},
		"unsupported": {"compress", []byte("opaque bytes")},
		"stacked":     {"gzip, br", gz},
	} {
		t.Run(name, func(t *testing.T) {
			tr, _ := openTraffic(t)
			tr.record(newFlow("api.example.com", "GET", "/x", nil, tc.body, withRespEncoding(tc.header)))
			_, resp, err := tr.Get(onlyExchangeID(t, tr))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(resp, "\n"+string(tc.body)) {
				t.Fatalf("body not shown as stored:\n%q", resp)
			}
		})
	}
}

// The target chooses the compression ratio: a small body that expands far past
// maxDecodedBody is cut there, for every coding.
func TestGetCapsDecodedBody(t *testing.T) {
	plain := bytes.Repeat([]byte("A"), 16*maxDecodedBody)
	for _, kind := range []string{"gzip", "deflate-zlib", "br", "zstd"} {
		t.Run(kind, func(t *testing.T) {
			wire := compress(t, kind, plain)
			if len(wire) > maxInlineBody {
				t.Fatalf("test data spilled: %d compressed bytes", len(wire))
			}
			header := strings.TrimSuffix(kind, "-zlib")
			tr, _ := openTraffic(t)
			tr.record(newFlow("bomb.example.com", "GET", "/", nil, wire, withRespEncoding(header)))
			_, resp, err := tr.Get(onlyExchangeID(t, tr))
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(resp, "A"); got != maxDecodedBody {
				t.Fatalf("decoded %d bytes, want the cap %d", got, maxDecodedBody)
			}
			if !strings.Contains(resp, "…[truncated] decoded "+header+" body") {
				t.Fatalf("no truncation note at the end: %q", resp[len(resp)-120:])
			}
		})
	}
}

// A compressed body above maxInlineBody lives in the blob store; its preview is
// decoded from the blob instead of showing compressed bytes.
func TestGetDecodesSpilledBodyPreview(t *testing.T) {
	// Random hex compresses to about half its size, so the compressed body
	// still crosses the inline threshold.
	raw := make([]byte, 2*maxInlineBody)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	const marker = "<!doctype html><title>spilled-and-decoded</title>"
	plain := []byte(marker + hex.EncodeToString(raw))
	wire := compress(t, "gzip", plain)
	if len(wire) <= maxInlineBody {
		t.Fatalf("test data not large enough: %d compressed bytes", len(wire))
	}
	tr, _ := openTraffic(t)
	tr.record(newFlow("big.example.com", "GET", "/", nil, wire, withRespType("text/html"), withRespEncoding("gzip")))

	_, resp, err := tr.Get(onlyExchangeID(t, tr))
	if err != nil {
		t.Fatal(err)
	}
	_, body, _ := strings.Cut(resp, "\n\n")
	if !strings.HasPrefix(body, marker) {
		t.Fatalf("preview not decoded: %q", body[:min(len(body), 80)])
	}
	preview, pointer, ok := strings.Cut(body, "\n…[truncated] @blob sha256:")
	if !ok || len(pointer) < 64 {
		t.Fatalf("blob pointer missing: %q", body[max(0, len(body)-120):])
	}
	if len(preview) != blobPreview {
		t.Fatalf("preview is %d bytes, want %d", len(preview), blobPreview)
	}
}
