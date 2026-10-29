package api

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/10/artifact/internal/content"
	"github.com/10/artifact/internal/render"
	"github.com/10/artifact/internal/store"
)

//go:embed review.html
var reviewFiles embed.FS
var reviewTemplate = template.Must(template.ParseFS(reviewFiles, "review.html"))

type reviewData struct {
	Title       string
	Version     int
	Latest      int
	ContentURL  string
	OriginalURL string
	ArtifactID  string
	Nonce       string
	Proposal    *store.Proposal
	Suggestion  *store.Suggestion
	Stale       bool
}

func (s *server) reviewVersion(r *http.Request) (*store.Artifact, *store.Version, error) {
	reference, n, ok := splitRef(r.PathValue("ref"))
	if !ok {
		return nil, nil, store.ErrNotFound
	}
	art, err := s.store.Get(r.Context(), reference)
	if err != nil {
		return nil, nil, err
	}
	if n == 0 {
		n = art.LatestVersion
	}
	v, err := s.store.Version(r.Context(), art.ID, n)
	return art, v, err
}

func (s *server) handleReview(w http.ResponseWriter, r *http.Request) {
	art, v, err := s.reviewVersion(r)
	if err != nil {
		s.serveFailure(w, r, err)
		return
	}
	// Pin the embedded document and submitted version together, even when the
	// recipient opened a floating /review link just before a concurrent update.
	path := "/" + art.ID + "@" + strconv.Itoa(v.N)
	s.writeReview(w, r, reviewData{Title: v.Filename, Version: v.N, Latest: art.LatestVersion,
		ContentURL: path + "/review/content", OriginalURL: path, ArtifactID: art.ID})
}

func (s *server) handleProposalPreview(w http.ResponseWriter, r *http.Request) {
	art, p, err := s.store.Proposal(r.Context(), r.PathValue("ref"), r.PathValue("proposal"))
	if err != nil {
		s.serveFailure(w, r, err)
		return
	}
	suggestion, err := s.store.Suggestion(r.Context(), art.ID, p.SuggestionID)
	if err != nil {
		s.serveFailure(w, r, err)
		return
	}
	original := "/" + art.ID + "@" + strconv.Itoa(p.BaseVersion)
	s.writeReview(w, r, reviewData{Title: p.Filename, Version: p.BaseVersion, Latest: art.LatestVersion,
		ContentURL: "/" + art.ID + "/proposals/" + p.ID + "/content", OriginalURL: original,
		ArtifactID: art.ID, Proposal: p, Suggestion: suggestion, Stale: art.LatestVersion != p.BaseVersion || suggestion.Status != "pending"})
}

func (s *server) writeReview(w http.ResponseWriter, r *http.Request, data reviewData) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		s.serveFailure(w, r, err)
		return
	}
	data.Nonce = hex.EncodeToString(nonce)
	var page bytes.Buffer
	if err := reviewTemplate.Execute(&page, data); err != nil {
		s.serveFailure(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+data.Nonce+"'; style-src 'unsafe-inline'; frame-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	_, _ = w.Write(page.Bytes())
}

func (s *server) handleReviewContent(w http.ResponseWriter, r *http.Request) {
	art, v, err := s.reviewVersion(r)
	if err != nil {
		s.serveFailure(w, r, err)
		return
	}
	s.writeReviewContent(w, r, s.store.BlobPath(art.ID, v.N), *v)
}

func (s *server) handleProposalContent(w http.ResponseWriter, r *http.Request) {
	_, p, err := s.store.Proposal(r.Context(), r.PathValue("ref"), r.PathValue("proposal"))
	if err != nil {
		s.serveFailure(w, r, err)
		return
	}
	t, _ := time.Parse(time.RFC3339, p.CreatedAt)
	s.writeReviewContent(w, r, s.store.ProposalPath(p.ArtifactID, p.ID), store.Version{
		Filename: p.Filename, ContentType: p.ContentType, Size: p.Size, CreatedAt: t,
	})
}

// This route is always sandboxed, even when opened outside its iframe. The
// published bytes and /raw route remain untouched. The small bridge carries
// text selection only, never credentials or requests to execute agent tools.
func (s *server) writeReviewContent(w http.ResponseWriter, r *http.Request, path string, v store.Version) {
	f, err := os.Open(path)
	if err != nil {
		s.serveFailure(w, r, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	markdown := content.IsMarkdown(v.Filename)
	if markdown || v.ContentType == content.TypeHTML || strings.HasPrefix(v.ContentType, "text/plain") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Put the bridge first: documents ending in an unclosed comment or script
		// cannot swallow it. A leading doctype keeps the viewer in standards mode.
		// No DOM privileges cross the iframe sandbox boundary.
		_, _ = io.WriteString(w, selectionBridge)
		if markdown {
			src, err := io.ReadAll(f)
			if err == nil {
				err = render.Markdown(w, v.Filename, src)
			}
			if err != nil {
				s.log.Error("render review content", "error", err)
			}
		} else if v.ContentType == content.TypeHTML {
			_, _ = io.Copy(w, f)
		} else {
			_, _ = io.WriteString(w, "<meta charset=utf-8><style>body{margin:2rem;font:15px/1.6 monospace}pre{white-space:pre-wrap;overflow-wrap:anywhere}</style><pre>")
			// Escape incrementally; logs can be as large as the upload limit.
			buf := make([]byte, 32<<10)
			for {
				n, err := f.Read(buf)
				if n > 0 {
					_, _ = io.WriteString(w, template.HTMLEscapeString(string(buf[:n])))
				}
				if err != nil {
					break
				}
			}
			_, _ = io.WriteString(w, "</pre>")
		}
		return
	}
	w.Header().Set("Content-Type", v.ContentType)
	w.Header().Set("Content-Disposition", content.ContentDisposition(content.Disposition(v.ContentType), v.Filename))
	http.ServeContent(w, r, "", v.CreatedAt, f)
}

const selectionBridge = `<!doctype html><script>
addEventListener('mouseup',sendSelection);addEventListener('keyup',sendSelection);
function sendSelection(){const quote=String(getSelection()).trim().slice(0,500);if(quote)parent.postMessage({type:'artifact-selection',quote},'*')}
</script>`

// Used in Link headers without changing uploaded bytes.
func reviewLink(reference string, n int) string {
	return fmt.Sprintf("</%s@%d/review>; rel=\"alternate\"; title=\"Suggest a change\"", reference, n)
}
