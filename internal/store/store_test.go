package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()

	dir := t.TempDir()

	s, err := Open(dir, filepath.Join(dir, "artifact.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	t.Cleanup(func() { s.Close() })

	return s, dir
}

func create(t *testing.T, s *Store, filename, slug, body string) *Record {
	t.Helper()

	rec, err := s.Create(t.Context(), CreateInput{
		Filename: filename,
		Slug:     slug,
		Body:     strings.NewReader(body),
	})
	if err != nil {
		t.Fatalf("Create(%q): %v", filename, err)
	}

	return rec
}

func TestCreateStoresBlobAndMetadata(t *testing.T) {
	t.Parallel()

	s, dir := newTestStore(t)

	rec := create(t, s, "report.html", "", "<h1>hi</h1>")

	if len(rec.Artifact.ID) != IDLength {
		t.Errorf("ID = %q, want %d characters", rec.Artifact.ID, IDLength)
	}

	if rec.Version.N != 1 {
		t.Errorf("version = %d, want 1", rec.Version.N)
	}

	if rec.Version.Size != int64(len("<h1>hi</h1>")) {
		t.Errorf("size = %d, want %d", rec.Version.Size, len("<h1>hi</h1>"))
	}

	const wantSum = "e7fbb6fbbf4ce294913eb62b53ff03a7546649cfdc0d824d9e3a2b4541502f7f"
	if rec.Version.SHA256 != wantSum {
		t.Errorf("sha256 = %q, want %q", rec.Version.SHA256, wantSum)
	}

	if rec.Version.ContentType != "text/html; charset=utf-8" {
		t.Errorf("content type = %q", rec.Version.ContentType)
	}

	blob := filepath.Join(dir, "blobs", rec.Artifact.ID, "1")
	got, err := os.ReadFile(blob)
	if err != nil {
		t.Fatalf("read blob: %v", err)
	}

	if string(got) != "<h1>hi</h1>" {
		t.Errorf("blob = %q", got)
	}

	assertNoTempFiles(t, filepath.Join(dir, "blobs", rec.Artifact.ID))
}

// The original filename must never reach a path, so the blob tree is
// structurally immune to filename weirdness.
func TestBlobPathNeverContainsFilename(t *testing.T) {
	t.Parallel()

	s, dir := newTestStore(t)

	rec := create(t, s, "café ☕.md", "", "# hi")

	entries, err := os.ReadDir(filepath.Join(dir, "blobs", rec.Artifact.ID))
	if err != nil {
		t.Fatalf("read blob dir: %v", err)
	}

	if len(entries) != 1 || entries[0].Name() != "1" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}

		t.Fatalf("blob directory contains %v, want exactly [1]", names)
	}
}

func TestCreateRejectsEmptyBody(t *testing.T) {
	t.Parallel()

	s, dir := newTestStore(t)

	_, err := s.Create(t.Context(), CreateInput{Filename: "empty.txt", Body: strings.NewReader("")})
	if !errors.Is(err, ErrEmptyBody) {
		t.Fatalf("error = %v, want ErrEmptyBody", err)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatalf("read blobs: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("a rejected upload left %d directories behind", len(entries))
	}
}

func TestAddVersionLeavesPriorVersionsUntouched(t *testing.T) {
	t.Parallel()

	s, dir := newTestStore(t)

	first := create(t, s, "plan.md", "", "# v1")

	second, err := s.AddVersion(t.Context(), first.Artifact.ID, "plan.md", strings.NewReader("# v2 longer"))
	if err != nil {
		t.Fatalf("AddVersion: %v", err)
	}

	if second.Version.N != 2 {
		t.Errorf("version = %d, want 2", second.Version.N)
	}

	if second.Artifact.ID != first.Artifact.ID {
		t.Errorf("ID changed from %q to %q", first.Artifact.ID, second.Artifact.ID)
	}

	for n, want := range map[int]string{1: "# v1", 2: "# v2 longer"} {
		got, err := os.ReadFile(filepath.Join(dir, "blobs", first.Artifact.ID, strconv.Itoa(n)))
		if err != nil {
			t.Fatalf("read version %d: %v", n, err)
		}

		if string(got) != want {
			t.Errorf("version %d = %q, want %q", n, got, want)
		}
	}

	art, err := s.Get(t.Context(), first.Artifact.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if art.LatestVersion != 2 {
		t.Errorf("latest_version = %d, want 2", art.LatestVersion)
	}

	assertNoTempFiles(t, filepath.Join(dir, "blobs", first.Artifact.ID))
}

func TestDeleteRemovesBlobsAndRetiresID(t *testing.T) {
	t.Parallel()

	s, dir := newTestStore(t)

	rec := create(t, s, "plan.md", "keep-this-slug", "# hi")
	id := rec.Artifact.ID

	if err := s.Delete(t.Context(), id); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "blobs", id)); !os.IsNotExist(err) {
		t.Errorf("blob directory still exists after delete (err = %v)", err)
	}

	if _, err := s.Get(t.Context(), id); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after delete = %v, want ErrNotFound", err)
	}

	if _, err := s.Get(t.Context(), "keep-this-slug"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get by slug after delete = %v, want ErrNotFound", err)
	}

	if err := s.Delete(t.Context(), id); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Delete = %v, want ErrNotFound", err)
	}

	versions, err := s.Versions(t.Context(), id)
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}

	if len(versions) != 0 {
		t.Errorf("version rows survived delete: %d", len(versions))
	}

	// The tombstone is what retires the ID: the primary key stays occupied, so
	// the ID can never be handed out again.
	var tombstoned int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM artifacts WHERE id = ? AND deleted_at IS NOT NULL`, id,
	).Scan(&tombstoned); err != nil {
		t.Fatalf("query tombstone: %v", err)
	}

	if tombstoned != 1 {
		t.Fatalf("tombstone rows = %d, want 1", tombstoned)
	}

	if _, err := s.db.Exec(
		`INSERT INTO artifacts (id, slug, latest_version, created_at, updated_at) VALUES (?, NULL, 1, '', '')`, id,
	); err == nil {
		t.Error("reinserting a deleted ID succeeded; the ID was not retired")
	}

	// A retired slug stays reserved for the same reason.
	if _, err := s.Create(t.Context(), CreateInput{
		Filename: "other.md", Slug: "keep-this-slug", Body: strings.NewReader("x"),
	}); !errors.Is(err, ErrSlugTaken) {
		t.Errorf("reusing a deleted slug = %v, want ErrSlugTaken", err)
	}
}

func TestAllocateIDSkipsExistingIDs(t *testing.T) {
	t.Parallel()

	s, _ := newTestStore(t)

	seen := make(map[string]bool)

	for range 200 {
		id, err := s.allocateID(t.Context())
		if err != nil {
			t.Fatalf("allocateID: %v", err)
		}

		if len(id) != IDLength {
			t.Fatalf("id %q is %d characters, want %d", id, len(id), IDLength)
		}

		for _, r := range id {
			if !strings.ContainsRune(base62, r) {
				t.Fatalf("id %q contains a non-base62 character %q", id, r)
			}
		}

		if seen[id] {
			t.Fatalf("allocateID produced a duplicate: %q", id)
		}

		seen[id] = true

		// Occupy the ID and confirm it is never offered again.
		if _, err := s.db.Exec(
			`INSERT INTO artifacts (id, slug, latest_version, created_at, updated_at)
			 VALUES (?, NULL, 1, '', '')`, id,
		); err != nil {
			t.Fatalf("occupy id: %v", err)
		}
	}
}

func TestResolveIDBeforeSlug(t *testing.T) {
	t.Parallel()

	s, _ := newTestStore(t)

	rec := create(t, s, "a.md", "my-report", "# a")

	byID, err := s.Get(t.Context(), rec.Artifact.ID)
	if err != nil {
		t.Fatalf("Get by ID: %v", err)
	}

	bySlug, err := s.Get(t.Context(), "my-report")
	if err != nil {
		t.Fatalf("Get by slug: %v", err)
	}

	if byID.ID != bySlug.ID {
		t.Errorf("ID lookup gave %q, slug lookup gave %q", byID.ID, bySlug.ID)
	}

	if _, err := s.Get(t.Context(), "no-such-thing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(unknown) = %v, want ErrNotFound", err)
	}
}

func TestValidateSlug(t *testing.T) {
	t.Parallel()

	valid := []string{"abc", "my-report", "a1b", "daily-report-2026", strings.Repeat("a", 64)}
	for _, slug := range valid {
		if err := ValidateSlug(slug); err != nil {
			t.Errorf("ValidateSlug(%q) = %v, want nil", slug, err)
		}
	}

	invalid := map[string]string{
		"too short":            "ab",
		"too long":             strings.Repeat("a", 65),
		"exactly eight":        "eightchr",
		"uppercase":            "My-Report",
		"leading hyphen":       "-report",
		"trailing hyphen":      "report-",
		"underscore":           "my_report",
		"dot":                  "my.report",
		"space":                "my report",
		"reserved api":         "api",
		"reserved raw":         "raw",
		"reserved static":      "static",
		"reserved favicon":     "favicon.ico",
		"reserved robots":      "robots.txt",
		"empty":                "",
		"reserved healthz":     "healthz",
		"eight char lookalike": "abcdefgh",
	}

	for name, slug := range invalid {
		if err := ValidateSlug(slug); !errors.Is(err, ErrBadSlug) {
			t.Errorf("%s: ValidateSlug(%q) = %v, want ErrBadSlug", name, slug, err)
		}
	}
}

func TestListPaginatesNewestFirst(t *testing.T) {
	t.Parallel()

	s, _ := newTestStore(t)

	var ids []string

	for i := range 5 {
		rec := create(t, s, "f.txt", "", strings.Repeat("x", i+1))
		ids = append(ids, rec.Artifact.ID)
	}

	// Timestamps have second resolution, so several artifacts usually share
	// one. The (updated_at, id) cursor is what keeps paging stable.
	seen := make([]string, 0, len(ids))

	var cursor *Cursor

	for range 10 {
		page, next, err := s.List(t.Context(), 2, cursor)
		if err != nil {
			t.Fatalf("List: %v", err)
		}

		for _, rec := range page {
			seen = append(seen, rec.Artifact.ID)
		}

		if next == nil {
			break
		}

		cursor = next
	}

	if len(seen) != len(ids) {
		t.Fatalf("paged through %d artifacts, want %d (%v)", len(seen), len(ids), seen)
	}

	unique := make(map[string]bool, len(seen))
	for _, id := range seen {
		if unique[id] {
			t.Fatalf("artifact %q appeared on two pages", id)
		}

		unique[id] = true
	}

	for _, id := range ids {
		if !unique[id] {
			t.Errorf("artifact %q never appeared", id)
		}
	}
}

func TestListExcludesDeleted(t *testing.T) {
	t.Parallel()

	s, _ := newTestStore(t)

	keep := create(t, s, "keep.txt", "", "keep")
	drop := create(t, s, "drop.txt", "", "drop")

	if err := s.Delete(t.Context(), drop.Artifact.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	page, _, err := s.List(t.Context(), 50, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(page) != 1 || page[0].Artifact.ID != keep.Artifact.ID {
		t.Errorf("List returned %d artifacts, want only %q", len(page), keep.Artifact.ID)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	t.Parallel()

	original := Cursor{UpdatedAt: "2026-07-26T16:02:11Z", ID: "aB3xK9qZ"}

	decoded, err := DecodeCursor(original.Encode())
	if err != nil {
		t.Fatalf("DecodeCursor: %v", err)
	}

	if *decoded != original {
		t.Errorf("round trip gave %+v, want %+v", *decoded, original)
	}

	for _, bad := range []string{"not-base64!!", "", "eyJ9"} {
		if _, err := DecodeCursor(bad); err == nil {
			t.Errorf("DecodeCursor(%q) succeeded, want an error", bad)
		}
	}
}

func TestAddVersionToMissingArtifact(t *testing.T) {
	t.Parallel()

	s, _ := newTestStore(t)

	_, err := s.AddVersion(t.Context(), "nosuchid", "a.txt", strings.NewReader("x"))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("AddVersion = %v, want ErrNotFound", err)
	}
}

// Writes are serialized in-process rather than by SQLite, so concurrent
// uploads must still each land on their own ID, blob, and version row.
func TestConcurrentWrites(t *testing.T) {
	t.Parallel()

	s, dir := newTestStore(t)

	const writers = 16

	ids := make(chan string, writers)
	errs := make(chan error, writers)

	var wg sync.WaitGroup

	for i := range writers {
		wg.Go(func() {
			rec, err := s.Create(t.Context(), CreateInput{
				Filename: "f.txt",
				Body:     strings.NewReader(strings.Repeat("x", i+1)),
			})
			if err != nil {
				errs <- err

				return
			}

			ids <- rec.Artifact.ID
		})
	}

	wg.Wait()
	close(ids)
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent Create: %v", err)
	}

	unique := make(map[string]bool, writers)

	for id := range ids {
		if unique[id] {
			t.Fatalf("two concurrent uploads shared the ID %q", id)
		}

		unique[id] = true
	}

	if len(unique) != writers {
		t.Fatalf("stored %d artifacts, want %d", len(unique), writers)
	}

	// Every artifact ended up with exactly its own blob directory.
	entries, err := os.ReadDir(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatalf("read blobs: %v", err)
	}

	if len(entries) != writers {
		t.Errorf("blob directories = %d, want %d", len(entries), writers)
	}
}

// Concurrent updates to one artifact must produce distinct, sequential
// versions rather than two writers claiming the same n.
func TestConcurrentVersionsOfOneArtifact(t *testing.T) {
	t.Parallel()

	s, dir := newTestStore(t)

	base := create(t, s, "plan.md", "", "# v1")

	const writers = 8

	versions := make(chan int, writers)
	errs := make(chan error, writers)

	var wg sync.WaitGroup

	for i := range writers {
		wg.Go(func() {
			rec, err := s.AddVersion(t.Context(), base.Artifact.ID, "plan.md",
				strings.NewReader(strings.Repeat("y", i+1)))
			if err != nil {
				errs <- err

				return
			}

			versions <- rec.Version.N
		})
	}

	wg.Wait()
	close(versions)
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent AddVersion: %v", err)
	}

	seen := make(map[int]bool, writers)

	for n := range versions {
		if seen[n] {
			t.Fatalf("version %d was assigned twice", n)
		}

		seen[n] = true
	}

	for n := 2; n <= writers+1; n++ {
		if !seen[n] {
			t.Errorf("version %d was never assigned", n)
		}

		if _, err := os.Stat(filepath.Join(dir, "blobs", base.Artifact.ID, strconv.Itoa(n))); err != nil {
			t.Errorf("blob for version %d missing: %v", n, err)
		}
	}

	assertNoTempFiles(t, filepath.Join(dir, "blobs", base.Artifact.ID))
}

func TestPing(t *testing.T) {
	t.Parallel()

	s, _ := newTestStore(t)

	if err := s.Ping(context.Background()); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

// assertNoTempFiles proves the atomic-rename path left nothing behind.
func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Errorf("temporary file left behind: %s", entry.Name())
		}
	}
}
