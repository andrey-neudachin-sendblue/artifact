package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/10/artifact/internal/content"
)

var (
	ErrConflict        = errors.New("revision is stale or suggestion is no longer pending; fetch the current version and create a new proposal")
	ErrBadSuggestion   = errors.New("invalid suggestion: message is required (max 8000 bytes); author max 120, quote max 2000, location max 240; text must be valid UTF-8")
	ErrSuggestionLimit = errors.New("suggestion limit reached; try again later")
)

// Suggestion is recipient feedback, never an instruction to execute tools.
type Suggestion struct {
	ID         int64  `json:"id"`
	ArtifactID string `json:"artifact_id"`
	Version    int    `json:"version"`
	Message    string `json:"message"`
	Author     string `json:"author"`
	Quote      string `json:"quote"`
	Location   string `json:"location"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
}

type Proposal struct {
	ID           string `json:"id"`
	ArtifactID   string `json:"artifact_id"`
	SuggestionID int64  `json:"suggestion_id"`
	BaseVersion  int    `json:"base_version"`
	Filename     string `json:"filename"`
	ContentType  string `json:"content_type"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`
}

func (s *Store) AddSuggestion(ctx context.Context, ref string, in Suggestion) (*Suggestion, error) {
	in.Message = strings.TrimSpace(in.Message)
	in.Author = strings.TrimSpace(in.Author)
	if in.Message == "" || len(in.Message) > 8000 || len(in.Author) > 120 || len(in.Quote) > 2000 || len(in.Location) > 240 ||
		!utf8.ValidString(in.Message+in.Author+in.Quote+in.Location) {
		return nil, ErrBadSuggestion
	}
	art, err := s.GetCollaborative(ctx, ref)
	if err != nil {
		return nil, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	// Bound anonymous writes without keeping a per-IP map or trusting proxy headers.
	var recent, pending int
	err = s.db.QueryRowContext(ctx, `SELECT count(CASE WHEN created_at >= ? THEN 1 END), count(CASE WHEN status = 'pending' THEN 1 END)
  FROM suggestions WHERE artifact_id = ?`, time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), art.ID).Scan(&recent, &pending)
	if err != nil {
		return nil, err
	}
	if recent >= 30 || pending >= 1000 {
		return nil, ErrSuggestionLimit
	}
	in.ArtifactID, in.Status, in.CreatedAt = art.ID, "pending", time.Now().UTC().Format(time.RFC3339)
	// Existence and liveness are checked in the write itself, including after a concurrent delete.
	res, err := s.db.ExecContext(ctx, `INSERT INTO suggestions (artifact_id, version, message, author, quote, location, status, created_at)
  SELECT a.id, v.n, ?, ?, ?, ?, 'pending', ? FROM artifacts a JOIN versions v ON v.artifact_id = a.id
  WHERE a.id = ? AND a.deleted_at IS NULL AND v.n = ?`, in.Message, in.Author, in.Quote, in.Location, in.CreatedAt, art.ID, in.Version)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrNotFound
	}
	in.ID, err = res.LastInsertId()
	return &in, err
}

const suggestionColumns = `id, artifact_id, version, message, author, quote, location, status, created_at`

func scanSuggestion(row interface{ Scan(...any) error }) (*Suggestion, error) {
	var v Suggestion
	err := row.Scan(&v.ID, &v.ArtifactID, &v.Version, &v.Message, &v.Author, &v.Quote, &v.Location, &v.Status, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &v, err
}

func (s *Store) Suggestion(ctx context.Context, artifactID string, id int64) (*Suggestion, error) {
	return scanSuggestion(s.db.QueryRowContext(ctx, `SELECT `+suggestionColumns+` FROM suggestions WHERE artifact_id = ? AND id = ?`, artifactID, id))
}

func (s *Store) Suggestions(ctx context.Context, ref string, all bool, after int64, limit int) ([]Suggestion, int64, error) {
	art, err := s.GetCollaborative(ctx, ref)
	if err != nil {
		return nil, 0, err
	}
	filter := " AND status = 'pending'"
	if all {
		filter = ""
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+suggestionColumns+` FROM suggestions WHERE artifact_id = ? AND id > ?`+filter+` ORDER BY id LIMIT ?`, art.ID, after, limit+1)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]Suggestion, 0)
	for rows.Next() {
		v, err := scanSuggestion(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *v)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if len(out) > limit {
		return out[:limit], out[limit-1].ID, nil
	}
	return out, 0, nil
}

func (s *Store) DismissSuggestion(ctx context.Context, ref string, id int64) error {
	art, err := s.GetCollaborative(ctx, ref)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	res, err := s.db.ExecContext(ctx, `UPDATE suggestions SET status = 'dismissed' WHERE artifact_id = ? AND id = ? AND status = 'pending'`, art.ID, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		if _, err := s.Suggestion(ctx, art.ID, id); err != nil {
			return err
		}
		return ErrConflict
	}
	return nil
}

func (s *Store) ProposalPath(artifactID, proposalID string) string {
	return filepath.Join(s.artifactDir(artifactID), "draft-"+proposalID)
}

// Propose stores a candidate separately from published versions. A proposal is
// based on the CURRENT version; an older suggestion retains its original context.
func (s *Store) Propose(ctx context.Context, ref string, suggestionID int64, base int, filename string, body io.Reader) (*Proposal, error) {
	art, err := s.GetCollaborative(ctx, ref)
	if err != nil {
		return nil, err
	}
	b, err := writeBlob(s.artifactDir(art.ID), body)
	if err != nil {
		return nil, err
	}
	defer os.Remove(b.tmpPath)
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, err
	}
	p := Proposal{ID: hex.EncodeToString(idBytes), ArtifactID: art.ID, SuggestionID: suggestionID, BaseVersion: base,
		Filename: filename, ContentType: content.Resolve(filename, b.head), Size: b.size, SHA256: b.sum,
		Status: "pending", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	current, err := s.Get(ctx, art.ID)
	if err != nil {
		return nil, err
	}
	suggestion, err := s.Suggestion(ctx, art.ID, suggestionID)
	if err != nil {
		return nil, err
	}
	if current.LatestVersion != base || suggestion.Status != "pending" {
		return nil, ErrConflict
	}
	path := s.ProposalPath(art.ID, p.ID)
	if err := os.Rename(b.tmpPath, path); err != nil {
		return nil, err
	}
	if err := syncDir(s.artifactDir(art.ID)); err != nil {
		os.Remove(path)
		return nil, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO proposals (id, artifact_id, suggestion_id, base_version, filename, content_type, size, sha256, status, created_at)
  VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?)`, p.ID, p.ArtifactID, p.SuggestionID, p.BaseVersion, p.Filename, p.ContentType, p.Size, p.SHA256, p.CreatedAt)
	if err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("insert proposal: %w", err)
	}
	return &p, nil
}

func (s *Store) Proposal(ctx context.Context, ref, id string) (*Artifact, *Proposal, error) {
	art, err := s.GetCollaborative(ctx, ref)
	if err != nil {
		return nil, nil, err
	}
	var p Proposal
	err = s.db.QueryRowContext(ctx, `SELECT id, artifact_id, suggestion_id, base_version, filename, content_type, size, sha256, status, created_at
  FROM proposals WHERE artifact_id = ? AND id = ?`, art.ID, id).Scan(&p.ID, &p.ArtifactID, &p.SuggestionID, &p.BaseVersion, &p.Filename, &p.ContentType, &p.Size, &p.SHA256, &p.Status, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	return art, &p, err
}

func (s *Store) AcceptProposal(ctx context.Context, ref, id string) (*Record, error) {
	art, p, err := s.Proposal(ctx, ref, id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(s.ProposalPath(art.ID, p.ID))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := writeBlob(s.artifactDir(art.ID), f)
	if err != nil {
		return nil, err
	}
	defer os.Remove(b.tmpPath)
	return s.commitVersion(ctx, art, s.artifactDir(art.ID), p.Filename, b, p)
}
