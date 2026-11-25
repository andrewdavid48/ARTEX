package traffic

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"database/sql"
	"fmt"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// Bodies are stored as they crossed the wire, so a response a browser asked for
// with Accept-Encoding: gzip, br, zstd is stored compressed. Get undoes that
// Content-Encoding for the reader; the stored bytes and the recorded headers
// are not changed.

const (
	// maxDecodedBody caps what one inline body may decode to. The target picks
	// the compression ratio: without a cap a few kilobytes could expand to
	// gigabytes in the server's memory.
	maxDecodedBody = 1024 * 1024
	// maxZstdWindow bounds the window a zstd frame may ask the decoder to
	// allocate. 8 MiB is the largest window browsers accept for the zstd
	// content coding (RFC 8878 §3.1.1.1.2 recommends it as the limit).
	maxZstdWindow = 8 * 1024 * 1024
)

// contentCoding returns the single compression coding head declares in
// Content-Encoding, lower-cased, or "" when there is none, it is not one
// decodeContent handles, or more than one layer is declared.
func contentCoding(head string) string {
	coding := ""
	for _, line := range strings.Split(head, "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "Content-Encoding") {
			continue
		}
		for _, v := range strings.Split(value, ",") {
			switch v = strings.ToLower(strings.TrimSpace(v)); v {
			case "", "identity":
			case "gzip", "x-gzip", "deflate", "br", "zstd":
				if coding != "" {
					return ""
				}
				coding = v
			default:
				return ""
			}
		}
	}
	return coding
}

// decodeContent reads at most limit decoded bytes of r, compressed with coding
// (one contentCoding returns). truncated reports that more followed.
func decodeContent(coding string, r io.Reader, limit int) (out []byte, truncated bool, err error) {
	var zr io.Reader
	switch coding {
	case "gzip", "x-gzip":
		zr, err = gzip.NewReader(r)
	case "deflate":
		// HTTP "deflate" is the zlib format (RFC 9110); some servers send a bare
		// deflate stream instead, told apart by the zlib header.
		br := bufio.NewReader(r)
		if h, _ := br.Peek(2); len(h) == 2 && h[0]&0x0f == 8 && h[0]>>4 <= 7 && (uint(h[0])<<8|uint(h[1]))%31 == 0 {
			zr, err = zlib.NewReader(br)
		} else {
			zr = flate.NewReader(br)
		}
	case "br":
		zr = brotli.NewReader(r)
	case "zstd":
		var d *zstd.Decoder
		d, err = zstd.NewReader(r, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxWindow(maxZstdWindow))
		if err == nil {
			defer d.Close()
			zr = d
		}
	default:
		return nil, false, fmt.Errorf("unsupported content-encoding: %q", coding)
	}
	if err == nil {
		out, err = io.ReadAll(io.LimitReader(zr, int64(limit)+1))
	}
	if err != nil {
		return nil, false, fmt.Errorf("%s body: %w", coding, err)
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// displayBody returns the body Get shows for one side of an exchange: body
// with the Content-Encoding declared in head undone. A body that spilled to the
// blob store is decoded from the blob, up to the same preview size as an
// uncompressed one. A body that declares no coding, or does not decode, is
// returned as stored.
func (t *Traffic) displayBody(head string, body []byte, blob sql.NullString) []byte {
	coding := contentCoding(head)
	if coding == "" || len(body) == 0 {
		return body
	}
	if blob.Valid && blob.String != "" {
		f, _, err := t.Blob(blob.String)
		if err != nil {
			return body
		}
		defer f.Close()
		out, _, err := decodeContent(coding, f, blobPreview)
		if err != nil {
			return body
		}
		// assembleRaw adds the truncation line and the blob pointer.
		return []byte(truncateUTF8(out, blobPreview))
	}
	out, truncated, err := decodeContent(coding, bytes.NewReader(body), maxDecodedBody)
	if err != nil {
		return body
	}
	if truncated {
		out = append([]byte(truncateUTF8(out, maxDecodedBody)),
			fmt.Sprintf("\n…[truncated] decoded %s body, first %d bytes shown", coding, maxDecodedBody)...)
	}
	return out
}
