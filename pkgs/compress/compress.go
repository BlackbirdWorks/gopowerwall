// Package compress provides HTTP compression middleware supporting zstd, brotli, and gzip.
package compress

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

// Supported encoding tokens.
const (
	EncodingZstd   = "zstd"
	EncodingBrotli = "br"
	EncodingGzip   = "gzip"
)

const (
	headerAcceptEncoding  = "Accept-Encoding"
	headerContentEncoding = "Content-Encoding"
	headerContentLength   = "Content-Length"
	headerContentType     = "Content-Type"
	headerVary            = "Vary"
)

var (
	// ErrUnsupportedEncoding indicates an unrecognized compression algorithm.
	ErrUnsupportedEncoding = errors.New("unsupported compression encoding")

	// ErrHijackNotSupported indicates that the underlying ResponseWriter does not support hijacking.
	ErrHijackNotSupported = errors.New("hijack not supported")
)

// Compressor manages reusable encoder pools for zstd, brotli, and gzip.
type Compressor struct {
	zstdPool   sync.Pool
	brotliPool sync.Pool
	gzipPool   sync.Pool
}

// NewCompressor initializes a Compressor with encoder pools.
func NewCompressor() *Compressor {
	return &Compressor{
		zstdPool: sync.Pool{
			New: func() any {
				w, _ := zstd.NewWriter(io.Discard, zstd.WithEncoderLevel(zstd.SpeedFastest))

				return w
			},
		},
		brotliPool: sync.Pool{
			New: func() any {
				return brotli.NewWriterLevel(io.Discard, brotli.BestSpeed)
			},
		},
		gzipPool: sync.Pool{
			New: func() any {
				w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)

				return w
			},
		},
	}
}

func (c *Compressor) acquireWriter(w io.Writer, encoding string) (io.WriteCloser, func()) {
	switch encoding {
	case EncodingZstd:
		zw, ok := c.zstdPool.Get().(*zstd.Encoder)
		if ok {
			zw.Reset(w)

			return zw, func() {
				_ = zw.Close()
				c.zstdPool.Put(zw)
			}
		}
	case EncodingBrotli:
		bw, ok := c.brotliPool.Get().(*brotli.Writer)
		if ok {
			bw.Reset(w)

			return bw, func() {
				_ = bw.Close()
				c.brotliPool.Put(bw)
			}
		}
	case EncodingGzip:
		gw, ok := c.gzipPool.Get().(*gzip.Writer)
		if ok {
			gw.Reset(w)

			return gw, func() {
				_ = gw.Close()
				c.gzipPool.Put(gw)
			}
		}
	}

	return nil, nil
}

// Negotiate selects the preferred encoding based on the Accept-Encoding header.
// Preference order: zstd > br > gzip.
func Negotiate(acceptEncoding string) string {
	if acceptEncoding == "" {
		return ""
	}
	if strings.Contains(acceptEncoding, EncodingZstd) {
		return EncodingZstd
	}
	if strings.Contains(acceptEncoding, EncodingBrotli) {
		return EncodingBrotli
	}
	if strings.Contains(acceptEncoding, EncodingGzip) {
		return EncodingGzip
	}

	return ""
}

// IsCompressible reports whether the MIME type is compressible.
func IsCompressible(contentType string) bool {
	if contentType == "" {
		return true
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	if strings.HasPrefix(mediaType, "application/json") ||
		strings.HasPrefix(mediaType, "application/javascript") ||
		strings.HasPrefix(mediaType, "application/xml") ||
		strings.HasPrefix(mediaType, "image/svg+xml") {
		return true
	}

	return false
}

type compressedWriter struct {
	http.ResponseWriter
	compressor *Compressor
	writer     io.WriteCloser
	releaseFn  func()
	encoding   string
	wroteHead  bool
}

func (cw *compressedWriter) initCompression() {
	if cw.wroteHead {
		return
	}
	cw.wroteHead = true

	ct := cw.Header().Get(headerContentType)
	if !IsCompressible(ct) || cw.Header().Get(headerContentEncoding) != "" {
		return
	}

	w, release := cw.compressor.acquireWriter(cw.ResponseWriter, cw.encoding)
	if w != nil {
		cw.writer = w
		cw.releaseFn = release
		cw.Header().Set(headerContentEncoding, cw.encoding)
		cw.Header().Del(headerContentLength)
	}
}

func (cw *compressedWriter) WriteHeader(statusCode int) {
	if !cw.wroteHead {
		cw.initCompression()
		cw.ResponseWriter.WriteHeader(statusCode)
	}
}

func (cw *compressedWriter) Write(p []byte) (int, error) {
	if !cw.wroteHead {
		cw.initCompression()
	}
	if cw.writer != nil {
		return cw.writer.Write(p)
	}

	return cw.ResponseWriter.Write(p)
}

func (cw *compressedWriter) Close() error {
	if cw.releaseFn != nil {
		cw.releaseFn()
		cw.releaseFn = nil
	}

	return nil
}

func (cw *compressedWriter) Flush() {
	if flusher, ok := cw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (cw *compressedWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := cw.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}

	return nil, nil, ErrHijackNotSupported
}

// Handler returns an HTTP middleware wrapping next with compression.
func (c *Compressor) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add(headerVary, headerAcceptEncoding)

		enc := Negotiate(r.Header.Get(headerAcceptEncoding))
		if enc == "" {
			next.ServeHTTP(w, r)

			return
		}

		cw := &compressedWriter{
			ResponseWriter: w,
			compressor:     c,
			encoding:       enc,
		}
		defer func() {
			_ = cw.Close()
		}()

		next.ServeHTTP(cw, r)
	})
}
