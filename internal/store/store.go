// Package store owns artifact metadata in SQLite and artifact bytes on the
// local filesystem.
//
// Writes are serialized in-process: SQLite is never asked to coordinate
// concurrent writers. Every upload is streamed to a temporary file, fsynced,
// and atomically renamed into place before its metadata row is committed, so a
// crash can leave an orphaned blob but never a row pointing at a missing file.
package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/10/artifact/internal/content"

	_ "modernc.org/sqlite" // pure-Go driver: required for CGO_ENABLED=0
)

// Errors callers are expected to distinguish.
var (
	ErrNotFound         = errors.New("artifact not found")
	ErrSlugTaken        = errors.New("slug already in use")
	ErrBadSlug          = errors.New("invalid slug")
	ErrEmptyBody        = errors.New("empty body")
	ErrBadType          = errors.New("type must be informative or collaborative")
	ErrNotCollaborative = errors.New("this artifact is informative; create with --collab to enable collaboration")
)

const (
	TypeInformative   = "informative"
	TypeCollaborative = "collaborative"
)

// Artifact is one published thing. Slug is empty when none was supplied.
type Artifact struct {
	Type          string
	ID            string
	Slug          string
	LatestVersion int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Version is one immutable revision of an artifact's bytes.
type Version struct {
	N           int
	Filename    string
	ContentType string
	Size        int64
	SHA256      string
	CreatedAt   time.Time
}

// Record pairs an artifact with a single version of it.
type Record struct {
	Artifact Artifact
	Version  Version
}

// Store is the metadata database plus the blob tree beside it.
type Store struct {
	db      *sql.DB
	dataDir string
	blobDir string

	// writeMu makes this process the single writer, as SQLite prefers.
	writeMu sync.Mutex
}

// A deleted artifact keeps its row with deleted_at set. The row is a tombstone:
// it retires the ID so a stale link can never resolve to a later upload, and it
// keeps the slug reserved for the same reason. Its versions rows and blobs are
// removed outright.
const schema = `
CREATE TABLE IF NOT EXISTS artifacts (
    id             TEXT    PRIMARY KEY,
    slug           TEXT    UNIQUE,
    kind           TEXT    NOT NULL DEFAULT 'informative' CHECK(kind IN ('informative', 'collaborative')),
    latest_version INTEGER NOT NULL,
    created_at     TEXT    NOT NULL,
    updated_at     TEXT    NOT NULL,
    deleted_at     TEXT
) STRICT;

CREATE TABLE IF NOT EXISTS versions (
    artifact_id  TEXT    NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
    n            INTEGER NOT NULL,
    filename     TEXT    NOT NULL,
    content_type TEXT    NOT NULL,
    size         INTEGER NOT NULL,
    sha256       TEXT    NOT NULL,
    created_at   TEXT    NOT NULL,
    PRIMARY KEY (artifact_id, n)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_artifacts_updated_at ON artifacts(updated_at DESC);

CREATE TABLE IF NOT EXISTS suggestions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    artifact_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    message TEXT NOT NULL,
    author TEXT NOT NULL,
    quote TEXT NOT NULL,
    location TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'addressed', 'dismissed')),
    created_at TEXT NOT NULL,
    FOREIGN KEY (artifact_id, version) REFERENCES versions(artifact_id, n) ON DELETE CASCADE
) STRICT;
CREATE INDEX IF NOT EXISTS idx_suggestions_inbox ON suggestions(artifact_id, status, id);

CREATE TABLE IF NOT EXISTS proposals (
    id TEXT PRIMARY KEY,
    artifact_id TEXT NOT NULL,
    suggestion_id INTEGER NOT NULL REFERENCES suggestions(id) ON DELETE CASCADE,
    base_version INTEGER NOT NULL,
    filename TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size INTEGER NOT NULL,
    sha256 TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'accepted')),
    created_at TEXT NOT NULL,
    FOREIGN KEY (artifact_id, base_version) REFERENCES versions(artifact_id, n) ON DELETE CASCADE
) STRICT;
`

// Open prepares dataDir, opens dbPath, and applies the schema.
func Open(dataDir, dbPath string) (*Store, error) {
	blobDir := filepath.Join(dataDir, "blobs")
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}

	dsn := "file:" + dbPath + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()

		return nil, fmt.Errorf("apply schema: %w", err)
	}

	if err := migrateArtifactTypes(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate artifact types: %w", err)
	}
	return &Store{db: db, dataDir: dataDir, blobDir: blobDir}, nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

// Ping reports whether the database answers and the data directory is
// writable. It backs GET /healthz.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	f, err := os.CreateTemp(s.dataDir, ".healthz-*")
	if err != nil {
		return fmt.Errorf("data directory not writable: %w", err)
	}

	name := f.Name()
	f.Close()
	os.Remove(name)

	return nil
}

// BlobPath is where version n of an artifact lives on disk.
func (s *Store) BlobPath(id string, n int) string {
	return filepath.Join(s.artifactDir(id), strconv.Itoa(n))
}

func (s *Store) artifactDir(id string) string {
	return filepath.Join(s.blobDir, id)
}

// CreateInput describes a new artifact. Filename must already be sanitized.
type CreateInput struct {
	Type     string
	Filename string
	Slug     string
	Body     io.Reader
}

// Create allocates an ID, stores the bytes durably, and commits version 1.
func (s *Store) Create(ctx context.Context, in CreateInput) (*Record, error) {
	if in.Type == "" {
		in.Type = TypeInformative
	}
	if in.Type != TypeInformative && in.Type != TypeCollaborative {
		return nil, ErrBadType
	}
	if in.Slug != "" {
		if err := ValidateSlug(in.Slug); err != nil {
			return nil, err
		}
	}

	id, err := s.allocateID(ctx)
	if err != nil {
		return nil, err
	}

	dir := s.artifactDir(id)

	b, err := writeBlob(dir, in.Body)
	if err != nil {
		os.RemoveAll(dir)

		return nil, err
	}

	rec, err := s.commitCreate(ctx, id, dir, in, b)
	if err != nil {
		// The whole directory belongs to an artifact that now does not
		// exist, so removing it wholesale is safe.
		os.RemoveAll(dir)

		return nil, err
	}

	return rec, nil
}

func (s *Store) commitCreate(ctx context.Context, id, dir string, in CreateInput, b *blob) (*Record, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if in.Slug != "" {
		var taken int

		err := tx.QueryRowContext(ctx, `SELECT 1 FROM artifacts WHERE slug = ?`, in.Slug).Scan(&taken)
		switch {
		case err == nil:
			return nil, ErrSlugTaken
		case !errors.Is(err, sql.ErrNoRows):
			return nil, fmt.Errorf("check slug: %w", err)
		}
	}

	now := time.Now().UTC().Truncate(time.Second)
	ts := now.Format(time.RFC3339)

	if err := publish(dir, b.tmpPath, 1); err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO artifacts (id, slug, kind, latest_version, created_at, updated_at) VALUES (?, ?, ?, 1, ?, ?)`,
		id, nullable(in.Slug), in.Type, ts, ts,
	); err != nil {
		return nil, fmt.Errorf("insert artifact: %w", err)
	}

	version := Version{
		N:           1,
		Filename:    in.Filename,
		ContentType: content.Resolve(in.Filename, b.head),
		Size:        b.size,
		SHA256:      b.sum,
		CreatedAt:   now,
	}

	if err := insertVersion(ctx, tx, id, version); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	return &Record{
		Artifact: Artifact{ID: id, Slug: in.Slug, Type: in.Type, LatestVersion: 1, CreatedAt: now, UpdatedAt: now},
		Version:  version,
	}, nil
}

// AddVersion stores new bytes for an existing artifact as version n+1. The
// artifact's ID, slug, and URL are unchanged, and prior versions are untouched.
func (s *Store) AddVersion(ctx context.Context, ref, filename string, body io.Reader) (*Record, error) {
	art, err := s.Get(ctx, ref)
	if err != nil {
		return nil, err
	}

	dir := s.artifactDir(art.ID)

	b, err := writeBlob(dir, body)
	if err != nil {
		return nil, err
	}

	rec, err := s.commitVersion(ctx, art, dir, filename, b, nil)
	if err != nil {
		os.Remove(b.tmpPath)

		return nil, err
	}

	return rec, nil
}

func (s *Store) commitVersion(ctx context.Context, art *Artifact, dir, filename string, b *blob, proposal *Proposal) (*Record, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Re-read under the write lock: the artifact may have been deleted, and
	// latest_version may have moved, while the body was streaming.
	var latest int
	err = tx.QueryRowContext(ctx,
		`SELECT latest_version FROM artifacts WHERE id = ? AND deleted_at IS NULL`, art.ID,
	).Scan(&latest)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("read latest version: %w", err)
	}

	if proposal != nil {
		var ready int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM proposals p JOIN suggestions s ON s.id = p.suggestion_id
			WHERE p.id = ? AND p.artifact_id = ? AND p.status = 'pending' AND s.status = 'pending'`,
			proposal.ID, art.ID).Scan(&ready)
		if errors.Is(err, sql.ErrNoRows) || latest != proposal.BaseVersion {
			return nil, ErrConflict
		}
		if err != nil {
			return nil, err
		}
	}

	n := latest + 1
	now := time.Now().UTC().Truncate(time.Second)
	ts := now.Format(time.RFC3339)

	if err := publish(dir, b.tmpPath, n); err != nil {
		return nil, err
	}

	version := Version{
		N:           n,
		Filename:    filename,
		ContentType: content.Resolve(filename, b.head),
		Size:        b.size,
		SHA256:      b.sum,
		CreatedAt:   now,
	}

	if err := insertVersion(ctx, tx, art.ID, version); err != nil {
		os.Remove(filepath.Join(dir, strconv.Itoa(n)))

		return nil, err
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE artifacts SET latest_version = ?, updated_at = ? WHERE id = ?`, n, ts, art.ID,
	); err != nil {
		os.Remove(filepath.Join(dir, strconv.Itoa(n)))

		return nil, fmt.Errorf("update artifact: %w", err)
	}

	if proposal != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE proposals SET status = 'accepted' WHERE id = ?`, proposal.ID); err != nil {
			os.Remove(filepath.Join(dir, strconv.Itoa(n)))
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE suggestions SET status = 'addressed' WHERE id = ?`, proposal.SuggestionID); err != nil {
			os.Remove(filepath.Join(dir, strconv.Itoa(n)))
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		os.Remove(filepath.Join(dir, strconv.Itoa(n)))

		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	updated := *art
	updated.LatestVersion = n
	updated.UpdatedAt = now

	return &Record{Artifact: updated, Version: version}, nil
}

// Delete retires the artifact. Metadata is committed before the blobs are
// removed, so the worst a crash can do is orphan bytes on disk.
func (s *Store) Delete(ctx context.Context, ref string) error {
	art, err := s.Get(ctx, ref)
	if err != nil {
		return err
	}

	if err := s.commitDelete(ctx, art.ID); err != nil {
		return err
	}

	if err := os.RemoveAll(s.artifactDir(art.ID)); err != nil {
		return fmt.Errorf("remove blobs: %w", err)
	}

	return nil
}

func (s *Store) commitDelete(ctx context.Context, id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`UPDATE artifacts SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL`,
		time.Now().UTC().Truncate(time.Second).Format(time.RFC3339), id,
	)
	if err != nil {
		return fmt.Errorf("mark artifact deleted: %w", err)
	}

	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM versions WHERE artifact_id = ?`, id); err != nil {
		return fmt.Errorf("delete versions: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// Get resolves a reference to a live artifact, trying the ID namespace before
// the slug namespace. The two are disjoint by length, so at most one can match.
func (s *Store) Get(ctx context.Context, ref string) (*Artifact, error) {
	art, err := s.lookup(ctx, `id = ?`, ref)
	if err == nil || !errors.Is(err, ErrNotFound) {
		return art, err
	}

	return s.lookup(ctx, `slug = ?`, ref)
}

func (s *Store) lookup(ctx context.Context, where, arg string) (*Artifact, error) {
	query := `SELECT id, slug, kind, latest_version, created_at, updated_at FROM artifacts WHERE ` +
		where + ` AND deleted_at IS NULL`

	var (
		art  Artifact
		slug sql.NullString
		created,
		updated string
	)

	err := s.db.QueryRowContext(ctx, query, arg).
		Scan(&art.ID, &slug, &art.Type, &art.LatestVersion, &created, &updated)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("read artifact: %w", err)
	}

	art.Slug = slug.String
	art.CreatedAt = parseTime(created)
	art.UpdatedAt = parseTime(updated)

	return &art, nil
}

// Version reads one revision of an artifact.
func (s *Store) Version(ctx context.Context, id string, n int) (*Version, error) {
	var (
		v       Version
		created string
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT n, filename, content_type, size, sha256, created_at
		   FROM versions WHERE artifact_id = ? AND n = ?`, id, n,
	).Scan(&v.N, &v.Filename, &v.ContentType, &v.Size, &v.SHA256, &created)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("read version: %w", err)
	}

	v.CreatedAt = parseTime(created)

	return &v, nil
}

// Versions reads an artifact's full history, oldest first.
func (s *Store) Versions(ctx context.Context, id string) ([]Version, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT n, filename, content_type, size, sha256, created_at
		   FROM versions WHERE artifact_id = ? ORDER BY n`, id)
	if err != nil {
		return nil, fmt.Errorf("list versions: %w", err)
	}
	defer rows.Close()

	var versions []Version

	for rows.Next() {
		var (
			v       Version
			created string
		)

		if err := rows.Scan(&v.N, &v.Filename, &v.ContentType, &v.Size, &v.SHA256, &created); err != nil {
			return nil, fmt.Errorf("scan version: %w", err)
		}

		v.CreatedAt = parseTime(created)
		versions = append(versions, v)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list versions: %w", err)
	}

	return versions, nil
}

// Cursor is the decoded form of the opaque pagination token.
type Cursor struct {
	UpdatedAt string `json:"u"`
	ID        string `json:"i"`
}

// Encode renders a cursor as the opaque string clients round-trip.
func (c Cursor) Encode() string {
	raw, err := json.Marshal(c)
	if err != nil {
		return ""
	}

	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeCursor parses a cursor produced by Cursor.Encode.
func DecodeCursor(s string) (*Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("malformed cursor: %w", err)
	}

	var c Cursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("malformed cursor: %w", err)
	}

	if c.UpdatedAt == "" || c.ID == "" {
		return nil, errors.New("malformed cursor")
	}

	return &c, nil
}

const listColumns = `a.id, a.slug, a.kind, a.latest_version, a.created_at, a.updated_at,
       v.n, v.filename, v.content_type, v.size, v.sha256, v.created_at`

// List returns live artifacts with their latest version, newest-updated first.
// The returned cursor is nil on the last page.
func (s *Store) List(ctx context.Context, limit int, after *Cursor) ([]Record, *Cursor, error) {
	// One extra row tells us whether another page exists.
	args := []any{limit + 1}
	filter := ""

	if after != nil {
		filter = `AND (a.updated_at < ? OR (a.updated_at = ? AND a.id < ?))`
		args = []any{after.UpdatedAt, after.UpdatedAt, after.ID, limit + 1}
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT `+listColumns+`
		  FROM artifacts a
		  JOIN versions v ON v.artifact_id = a.id AND v.n = a.latest_version
		 WHERE a.deleted_at IS NULL `+filter+`
		 ORDER BY a.updated_at DESC, a.id DESC
		 LIMIT ?`, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("list artifacts: %w", err)
	}
	defer rows.Close()

	records := make([]Record, 0, limit)

	for rows.Next() {
		var (
			rec                                Record
			slug                               sql.NullString
			artCreated, artUpdated, verCreated string
		)

		if err := rows.Scan(
			&rec.Artifact.ID, &slug, &rec.Artifact.Type, &rec.Artifact.LatestVersion, &artCreated, &artUpdated,
			&rec.Version.N, &rec.Version.Filename, &rec.Version.ContentType,
			&rec.Version.Size, &rec.Version.SHA256, &verCreated,
		); err != nil {
			return nil, nil, fmt.Errorf("scan artifact: %w", err)
		}

		rec.Artifact.Slug = slug.String
		rec.Artifact.CreatedAt = parseTime(artCreated)
		rec.Artifact.UpdatedAt = parseTime(artUpdated)
		rec.Version.CreatedAt = parseTime(verCreated)

		records = append(records, rec)
	}

	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("list artifacts: %w", err)
	}

	if len(records) <= limit {
		return records, nil, nil
	}

	records = records[:limit]
	last := records[len(records)-1].Artifact

	return records, &Cursor{UpdatedAt: last.UpdatedAt.Format(time.RFC3339), ID: last.ID}, nil
}

// allocateID returns an ID that no artifact has ever held. Tombstones stay in
// the table, so a deleted ID is never handed out again.
func (s *Store) allocateID(ctx context.Context) (string, error) {
	for range 8 {
		id, err := newID()
		if err != nil {
			return "", err
		}

		var exists int

		err = s.db.QueryRowContext(ctx, `SELECT 1 FROM artifacts WHERE id = ?`, id).Scan(&exists)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return id, nil
		case err != nil:
			return "", fmt.Errorf("check id: %w", err)
		}
	}

	return "", errors.New("could not allocate an unused artifact id")
}

func insertVersion(ctx context.Context, tx *sql.Tx, id string, v Version) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO versions (artifact_id, n, filename, content_type, size, sha256, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, v.N, v.Filename, v.ContentType, v.Size, v.SHA256, v.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("insert version: %w", err)
	}

	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}

	return s
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}

	return t.UTC()
}

// Existing uploads without feedback become informative. Artifacts with an
// established suggestion/revision workflow keep that workflow through upgrade.
func migrateArtifactTypes(db *sql.DB) error {
	var exists int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('artifacts') WHERE name = 'kind'`).Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`ALTER TABLE artifacts ADD COLUMN kind TEXT NOT NULL DEFAULT 'informative' CHECK(kind IN ('informative', 'collaborative'))`); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE artifacts SET kind = 'collaborative' WHERE EXISTS (SELECT 1 FROM suggestions WHERE artifact_id = artifacts.id)`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetCollaborative(ctx context.Context, ref string) (*Artifact, error) {
	art, err := s.Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	if art.Type != TypeCollaborative {
		return nil, ErrNotCollaborative
	}
	return art, nil
}
