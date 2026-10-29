package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/10/artifact/internal/store"
)

// Stable error codes. A skill branches on these rather than on prose.
const (
	codeBadRequest      = "bad_request"
	codeUnauthorized    = "unauthorized"
	codeNotFound        = "not_found"
	codeSlugTaken       = "slug_taken"
	codePayloadTooLarge = "payload_too_large"
	codeInternal        = "internal"
)

type errorEnvelope struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// artifactPayload is the response body shared by upload, update, and each
// entry of the list response.
type artifactPayload struct {
	Type        string  `json:"type"`
	ID          string  `json:"id"`
	Slug        *string `json:"slug"`
	URL         string  `json:"url"`
	RawURL      string  `json:"raw_url"`
	ReviewURL   string  `json:"review_url,omitempty"`
	Version     int     `json:"version"`
	VersionURL  string  `json:"version_url"`
	Filename    string  `json:"filename"`
	ContentType string  `json:"content_type"`
	Size        int64   `json:"size"`
	SHA256      string  `json:"sha256"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

type listPayload struct {
	Artifacts  []artifactPayload `json:"artifacts"`
	NextCursor *string           `json:"next_cursor"`
}

type versionPayload struct {
	N           int    `json:"n"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	CreatedAt   string `json:"created_at"`
	URL         string `json:"url"`
}

type infoPayload struct {
	Type          string           `json:"type"`
	ID            string           `json:"id"`
	Slug          *string          `json:"slug"`
	URL           string           `json:"url"`
	ReviewURL     string           `json:"review_url,omitempty"`
	LatestVersion int              `json:"latest_version"`
	CreatedAt     string           `json:"created_at"`
	UpdatedAt     string           `json:"updated_at"`
	Versions      []versionPayload `json:"versions"`
}

// ref is the canonical path element for an artifact: its slug when it has one,
// otherwise its ID. Both always resolve.
func ref(a store.Artifact) string {
	if a.Slug != "" {
		return a.Slug
	}

	return a.ID
}

func (s *server) artifactURL(a store.Artifact) string {
	return s.baseURL + "/" + ref(a)
}

func (s *server) versionURL(a store.Artifact, n int) string {
	return s.artifactURL(a) + "@" + strconv.Itoa(n)
}

func (s *server) recordPayload(rec store.Record) artifactPayload {
	return artifactPayload{
		ID:          rec.Artifact.ID,
		Type:        rec.Artifact.Type,
		Slug:        optional(rec.Artifact.Slug),
		URL:         s.artifactURL(rec.Artifact),
		RawURL:      s.artifactURL(rec.Artifact) + "/raw",
		ReviewURL:   s.reviewURL(rec.Artifact),
		Version:     rec.Version.N,
		VersionURL:  s.versionURL(rec.Artifact, rec.Version.N),
		Filename:    rec.Version.Filename,
		ContentType: rec.Version.ContentType,
		Size:        rec.Version.Size,
		SHA256:      rec.Version.SHA256,
		CreatedAt:   stamp(rec.Artifact.CreatedAt),
		UpdatedAt:   stamp(rec.Artifact.UpdatedAt),
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)

	// A write failure here means the client is gone; there is nothing useful
	// left to say on this connection.
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: errorDetail{Code: code, Message: message}})
}

// internalError logs the cause and returns a response that does not leak it.
func (s *server) internalError(r *http.Request, w http.ResponseWriter, op string, err error) {
	s.log.Error(op, "error", err, "method", r.Method, "path", r.URL.Path)
	writeError(w, http.StatusInternalServerError, codeInternal, "internal server error")
}

func optional(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}

func stamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func (s *server) reviewURL(art store.Artifact) string {
	if art.Type != store.TypeCollaborative {
		return ""
	}
	return s.artifactURL(art) + "/review"
}
