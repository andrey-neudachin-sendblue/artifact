// Package api is the HTTP surface: routing, bearer auth, the JSON API, and
// the public artifact routes.
package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/10/artifact/internal/store"
)

// Config is everything the handler needs from the environment.
type Config struct {
	Token         string
	BaseURL       string
	MaxUploadSize int64
	Logger        *slog.Logger
}

type server struct {
	store     *store.Store
	baseURL   string
	tokenHash [sha256.Size]byte
	maxUpload int64
	log       *slog.Logger
}

// New builds the complete handler.
func New(st *store.Store, cfg Config) http.Handler {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	s := &server{
		store:     st,
		baseURL:   strings.TrimRight(cfg.BaseURL, "/"),
		tokenHash: sha256.Sum256([]byte(cfg.Token)),
		maxUpload: cfg.MaxUploadSize,
		log:       log,
	}

	mux := http.NewServeMux()

	// Each API route is registered individually rather than behind an /api/
	// prefix: a prefix pattern would overlap GET /{ref}/{name} without being
	// more specific than it, which ServeMux rejects as ambiguous.
	protected := func(pattern string, handler http.HandlerFunc) {
		mux.Handle(pattern, s.requireToken(handler))
	}

	protected("POST /api/upload", s.handleUpload)
	protected("PUT /api/artifacts/{id}", s.handleUpdate)
	protected("DELETE /api/artifacts/{id}", s.handleDelete)
	protected("GET /api/artifacts", s.handleList)
	protected("GET /api/artifacts/{id}", s.handleInfo)

	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /{ref}", s.handleServe)
	mux.HandleFunc("GET /{ref}/{name}", s.handleServe)

	return s.logRequests(mux)
}

// requireToken rejects a request before anything reads its body, so a bad
// token never consumes an upload.
func (s *server) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "missing or invalid token")

			return
		}

		next.ServeHTTP(w, r)
	})
}

// authorized compares SHA-256 digests rather than the tokens themselves: the
// comparison is constant-time and its duration does not depend on token length.
func (s *server) authorized(r *http.Request) bool {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return false
	}

	got := sha256.Sum256([]byte(strings.TrimSpace(token)))

	return subtle.ConstantTimeCompare(got[:], s.tokenHash[:]) == 1
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		s.log.Error("health check failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})

		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// requestInfo carries values a handler discovers into the access log.
type requestInfo struct {
	artifactID string
}

type ctxKey struct{}

// noteArtifact records which artifact a request touched, for the access log.
func noteArtifact(r *http.Request, id string) {
	if info, ok := r.Context().Value(ctxKey{}).(*requestInfo); ok {
		info.artifactID = id
	}
}

// logRequests emits one structured line per request. It never logs the
// Authorization header or any request body.
func (s *server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		info := &requestInfo{}
		rec := &recorder{ResponseWriter: w}

		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), ctxKey{}, info)))

		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.statusCode(),
			"bytes", rec.written,
			"duration_ms", time.Since(started).Milliseconds(),
		}
		if info.artifactID != "" {
			attrs = append(attrs, "artifact_id", info.artifactID)
		}

		s.log.Info("request", attrs...)
	})
}

// recorder captures the status and byte count for the access log.
type recorder struct {
	http.ResponseWriter

	status  int
	written int64
}

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}

	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.written += int64(n)

	return n, err
}

// ReadFrom keeps net/http's sendfile path reachable through the wrapper.
// io.Copy inside http.ServeContent looks for io.ReaderFrom on the writer, and
// without this the wrapper would silently downgrade every large download —
// exactly the video and screen-recording case Artifact exists to serve.
func (r *recorder) ReadFrom(src io.Reader) (int64, error) {
	dst := io.Writer(r.ResponseWriter)
	if from, ok := r.ResponseWriter.(io.ReaderFrom); ok {
		n, err := from.ReadFrom(src)
		r.written += n

		return n, err
	}

	n, err := io.Copy(dst, src)
	r.written += n

	return n, err
}

func (r *recorder) statusCode() int {
	if r.status == 0 {
		return http.StatusOK
	}

	return r.status
}
