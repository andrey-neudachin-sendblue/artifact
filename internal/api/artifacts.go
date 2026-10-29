package api

import (
	"net/http"
	"strconv"

	"github.com/10/artifact/internal/store"
)

const (
	defaultLimit = 50
	maxLimit     = 200
)

func (s *server) handleDelete(w http.ResponseWriter, r *http.Request) {
	noteArtifact(r, r.PathValue("id"))

	if err := s.store.Delete(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, r, "delete artifact", err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleList(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	limit := defaultLimit

	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			s.fail(w, r, "", badRequest("limit must be a positive integer"))

			return
		}

		limit = min(n, maxLimit)
	}

	var cursor *store.Cursor

	if raw := query.Get("cursor"); raw != "" {
		c, err := store.DecodeCursor(raw)
		if err != nil {
			s.fail(w, r, "", badRequest("invalid cursor"))

			return
		}

		cursor = c
	}

	records, next, err := s.store.List(r.Context(), limit, cursor)
	if err != nil {
		s.internalError(r, w, "list artifacts", err)

		return
	}

	payload := listPayload{Artifacts: make([]artifactPayload, 0, len(records))}
	for _, rec := range records {
		payload.Artifacts = append(payload.Artifacts, s.recordPayload(rec))
	}

	if next != nil {
		payload.NextCursor = optional(next.Encode())
	}

	writeJSON(w, http.StatusOK, payload)
}

func (s *server) handleInfo(w http.ResponseWriter, r *http.Request) {
	art, err := s.store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, "read artifact", err)

		return
	}

	noteArtifact(r, art.ID)

	versions, err := s.store.Versions(r.Context(), art.ID)
	if err != nil {
		s.internalError(r, w, "list versions", err)

		return
	}

	payload := infoPayload{
		ID:            art.ID,
		Slug:          optional(art.Slug),
		URL:           s.artifactURL(*art),
		ReviewURL:     s.artifactURL(*art) + "/review",
		LatestVersion: art.LatestVersion,
		CreatedAt:     stamp(art.CreatedAt),
		UpdatedAt:     stamp(art.UpdatedAt),
		Versions:      make([]versionPayload, 0, len(versions)),
	}

	for _, v := range versions {
		payload.Versions = append(payload.Versions, versionPayload{
			N:           v.N,
			Filename:    v.Filename,
			ContentType: v.ContentType,
			Size:        v.Size,
			SHA256:      v.SHA256,
			CreatedAt:   stamp(v.CreatedAt),
			URL:         s.versionURL(*art, v.N),
		})
	}

	writeJSON(w, http.StatusOK, payload)
}
