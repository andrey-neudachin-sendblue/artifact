package api

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/10/artifact/internal/store"
)

func (s *server) handleSuggest(w http.ResponseWriter, r *http.Request) {
	// JSON-only submissions cannot be sent by a cross-site HTML form. No CORS
	// permission is granted to untrusted origins (including sandboxed artifacts).
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "application/json" {
		s.fail(w, r, "suggestion", badRequest("Content-Type must be application/json"))
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		base, _ := url.Parse(s.baseURL)
		if base == nil || origin != base.Scheme+"://"+base.Host {
			writeError(w, http.StatusForbidden, "forbidden", "submit feedback from the review page")
			return
		}
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil {
		s.fail(w, r, "read suggestion", err)
		return
	}
	if !utf8.Valid(body) {
		s.fail(w, r, "suggestion", badRequest("suggestion must be valid UTF-8"))
		return
	}
	var in struct {
		Version  int    `json:"version"`
		Message  string `json:"message"`
		Author   string `json:"author"`
		Quote    string `json:"quote"`
		Location string `json:"location"`
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		s.fail(w, r, "suggestion", badRequest("invalid suggestion JSON"))
		return
	}
	if err := d.Decode(new(any)); err != io.EOF {
		s.fail(w, r, "suggestion", badRequest("expected one suggestion"))
		return
	}
	if in.Version < 1 {
		s.fail(w, r, "suggestion", badRequest("an explicit positive version is required"))
		return
	}
	v, err := s.store.AddSuggestion(r.Context(), r.PathValue("id"), store.Suggestion{
		Version: in.Version, Message: in.Message, Author: in.Author, Quote: in.Quote, Location: in.Location,
	})
	if err != nil {
		s.fail(w, r, "add suggestion", err)
		return
	}
	noteArtifact(r, v.ArtifactID)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, v)
}

// suggestionPayload includes immutable source URLs so an agent can inspect the
// version the recipient actually saw, even if the latest version has moved.
type suggestionPayload struct {
	store.Suggestion
	VersionURL string `json:"version_url"`
	RawURL     string `json:"raw_url"`
}

func (s *server) handleSuggestions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := defaultLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			s.fail(w, r, "", badRequest("limit must be positive"))
			return
		}
		limit = min(n, maxLimit)
	}
	var after int64
	if raw := q.Get("after"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			s.fail(w, r, "", badRequest("after must be a nonnegative integer"))
			return
		}
		after = n
	}
	all := q.Get("all") == "true"
	art, err := s.store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, "read artifact", err)
		return
	}
	suggestions, next, err := s.store.Suggestions(r.Context(), art.ID, all, after, limit)
	if err != nil {
		s.fail(w, r, "list suggestions", err)
		return
	}
	out := make([]suggestionPayload, 0, len(suggestions))
	for _, v := range suggestions {
		versionURL := s.versionURL(*art, v.Version)
		out = append(out, suggestionPayload{Suggestion: v, VersionURL: versionURL, RawURL: versionURL + "/raw"})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		Suggestions   []suggestionPayload `json:"suggestions"`
		NextAfter     int64               `json:"next_after"`
		LatestVersion int                 `json:"latest_version"`
	}{out, next, art.LatestVersion})
}

func suggestionID(r *http.Request) (int64, error) {
	n, err := strconv.ParseInt(r.PathValue("suggestion"), 10, 64)
	if err != nil || n < 1 {
		return 0, badRequest("invalid suggestion ID")
	}
	return n, nil
}

func (s *server) handleDismiss(w http.ResponseWriter, r *http.Request) {
	id, err := suggestionID(r)
	if err == nil {
		err = s.store.DismissSuggestion(r.Context(), r.PathValue("id"), id)
	}
	if err != nil {
		s.fail(w, r, "dismiss suggestion", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type proposalPayload struct {
	store.Proposal
	PreviewURL string `json:"preview_url"`
}

func (s *server) handlePropose(w http.ResponseWriter, r *http.Request) {
	id, err := suggestionID(r)
	if err != nil {
		s.fail(w, r, "proposal", err)
		return
	}
	base, err := strconv.Atoi(r.URL.Query().Get("base_version"))
	if err != nil || base < 1 {
		s.fail(w, r, "proposal", badRequest("base_version must be a positive integer"))
		return
	}
	up, err := s.readUpload(w, r)
	if err != nil {
		s.fail(w, r, "read proposal", err)
		return
	}
	defer up.release()
	p, err := s.store.Propose(r.Context(), r.PathValue("id"), id, base, up.filename, up.body)
	if err != nil {
		s.fail(w, r, "propose revision", err)
		return
	}
	// An ID is also a stable reference if the artifact was published with a slug.
	writeJSON(w, http.StatusCreated, proposalPayload{Proposal: *p, PreviewURL: s.baseURL + "/" + p.ArtifactID + "/proposals/" + p.ID})
}

func (s *server) handleAccept(w http.ResponseWriter, r *http.Request) {
	rec, err := s.store.AcceptProposal(r.Context(), r.PathValue("id"), r.PathValue("proposal"))
	if err != nil {
		s.fail(w, r, "accept revision", err)
		return
	}
	writeJSON(w, http.StatusOK, s.recordPayload(*rec))
}
