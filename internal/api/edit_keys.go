package api

import (
	"net/http"
	"strings"
)

func bearer(r *http.Request) string {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

// Edit capabilities never authorize upload, list, delete or key management.
func (s *server) requireArtifactAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !s.authorized(r) {
			ok, err := s.store.CheckEditKey(r.Context(), r.PathValue("id"), bearer(r))
			if err != nil || !ok {
				writeError(w, http.StatusUnauthorized, codeUnauthorized, "a valid edit key is required for this artifact")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) handleEditLink(w http.ResponseWriter, r *http.Request) {
	art, err := s.store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, "read artifact", err)
		return
	}
	key, err := s.store.IssueEditKey(r.Context(), art.ID, r.URL.Query().Get("rotate") == "true")
	if err != nil {
		s.fail(w, r, "issue edit key", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]string{
		"view_url": s.artifactURL(*art),
		"edit_url": s.artifactURL(*art) + "/edit#key=" + key,
	})
}
