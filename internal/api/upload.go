package api

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/10/artifact/internal/content"
	"github.com/10/artifact/internal/store"
)

// multipartMemory is how much of a multipart body is held in memory before
// Go spills the rest to a temporary file. Multipart is the fallback form; the
// preferred one streams a raw body straight to the blob.
const multipartMemory = 4 << 20

// apiError is a failure that already knows how it should be reported.
type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string { return e.message }

func badRequest(format string, args ...any) *apiError {
	return &apiError{
		status:  http.StatusBadRequest,
		code:    codeBadRequest,
		message: fmt.Sprintf(format, args...),
	}
}

// upload is a request body resolved into the pieces the store needs.
type upload struct {
	filename string
	kind     string
	slug     string
	body     io.Reader
	release  func()
}

func (s *server) handleUpload(w http.ResponseWriter, r *http.Request) {
	up, err := s.readUpload(w, r)
	if err != nil {
		s.fail(w, r, "read upload", err)

		return
	}
	defer up.release()

	rec, err := s.store.Create(r.Context(), store.CreateInput{
		Filename: up.filename,
		Type:     up.kind,
		Slug:     up.slug,
		Body:     up.body,
	})
	if err != nil {
		s.fail(w, r, "create artifact", err)

		return
	}

	noteArtifact(r, rec.Artifact.ID)
	key, err := s.store.IssueEditKey(r.Context(), rec.Artifact.ID, false)
	if err != nil {
		s.fail(w, r, "issue edit key", err)
		return
	}
	payload := s.recordPayload(*rec)
	payload.EditURL = s.artifactURL(rec.Artifact) + "/edit#key=" + key
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, payload)
}

func (s *server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	noteArtifact(r, r.PathValue("id"))

	up, err := s.readUpload(w, r)
	if err != nil {
		s.fail(w, r, "read upload", err)

		return
	}
	defer up.release()

	if up.kind != "" {
		s.fail(w, r, "update type", badRequest("artifact type is fixed at creation"))
		return
	}
	// A slug belongs to the artifact, not to a version, so it is ignored here.
	var rec *store.Record
	base, parseErr := strconv.Atoi(r.URL.Query().Get("base_version"))
	if r.URL.Query().Has("base_version") && (parseErr != nil || base < 1) {
		s.fail(w, r, "update", badRequest("base_version must be a positive integer"))
		return
	}
	if !s.authorized(r) && base < 1 {
		s.fail(w, r, "update", badRequest("base_version is required when editing with an edit key"))
		return
	}
	if base > 0 {
		rec, err = s.store.AddVersionAt(r.Context(), r.PathValue("id"), up.filename, up.body, base)
	} else {
		rec, err = s.store.AddVersion(r.Context(), r.PathValue("id"), up.filename, up.body)
	}
	if err != nil {
		s.fail(w, r, "add version", err)

		return
	}

	writeJSON(w, http.StatusOK, s.recordPayload(*rec))
}

// readUpload accepts both request forms. The client's Content-Type is used
// only to tell them apart; it never influences the stored content type.
func (s *server) readUpload(w http.ResponseWriter, r *http.Request) (*upload, error) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload)

	query := r.URL.Query()

	up := &upload{
		filename: query.Get("filename"),
		kind:     query.Get("type"),
		slug:     query.Get("slug"),
		body:     r.Body,
		release:  func() {},
	}

	if isMultipart(r) {
		if err := r.ParseMultipartForm(multipartMemory); err != nil {
			return nil, err
		}

		up.release = func() { r.MultipartForm.RemoveAll() }

		file, header, err := r.FormFile("file")
		if err != nil {
			up.release()

			return nil, badRequest(`multipart upload requires a "file" field`)
		}

		up.body = file
		up.filename = firstNonEmpty(multipartValue(r, "filename"), up.filename, header.Filename)
		up.slug = firstNonEmpty(multipartValue(r, "slug"), up.slug)
		up.kind = firstNonEmpty(multipartValue(r, "type"), up.kind)
	}

	if up.filename == "" {
		up.release()

		return nil, badRequest("filename is required")
	}

	name, err := content.SanitizeFilename(up.filename)
	if err != nil {
		up.release()

		return nil, err
	}

	up.filename = name

	return up, nil
}

// fail turns any error from the store or the request into the documented
// status and error code.
func (s *server) fail(w http.ResponseWriter, r *http.Request, op string, err error) {
	var (
		apiErr   *apiError
		tooLarge *http.MaxBytesError
	)

	switch {
	case errors.As(err, &apiErr):
		writeError(w, apiErr.status, apiErr.code, apiErr.message)
	case errors.As(err, &tooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, codePayloadTooLarge,
			fmt.Sprintf("upload exceeds the %d byte limit", tooLarge.Limit))
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "artifact not found")
	case errors.Is(err, store.ErrSlugTaken):
		writeError(w, http.StatusConflict, codeSlugTaken, "slug already in use")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "revision_conflict", err.Error())
	case errors.Is(err, store.ErrSuggestionLimit):
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "suggestion_limit", err.Error())
	case errors.Is(err, store.ErrBadSlug),
		errors.Is(err, store.ErrBadType),
		errors.Is(err, store.ErrBadSuggestion),
		errors.Is(err, store.ErrEmptyBody),
		errors.Is(err, content.ErrBadFilename):
		writeError(w, http.StatusBadRequest, codeBadRequest, err.Error())
	default:
		s.internalError(r, w, op, err)
	}
}

func isMultipart(r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))

	return err == nil && mediaType == "multipart/form-data"
}

func multipartValue(r *http.Request, key string) string {
	if r.MultipartForm == nil {
		return ""
	}

	if values := r.MultipartForm.Value[key]; len(values) > 0 {
		return values[0]
	}

	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}

	return ""
}
