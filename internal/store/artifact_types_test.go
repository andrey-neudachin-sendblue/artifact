package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyArtifactTypeMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact.db")
	st, err := Open(dir, path)
	if err != nil {
		t.Fatal(err)
	}
	informative, err := st.Create(t.Context(), CreateInput{Filename: "report.md", Body: strings.NewReader("# Report")})
	if err != nil {
		t.Fatal(err)
	}
	collaborative, err := st.Create(t.Context(), CreateInput{Type: TypeCollaborative, Filename: "draft.md", Body: strings.NewReader("# Draft")})
	if err != nil {
		t.Fatal(err)
	}
	suggestion, err := st.AddSuggestion(t.Context(), collaborative.Artifact.ID, Suggestion{Version: 1, Message: "Keep my feedback"})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	// Remove only the new column to reproduce a database from the prior release.
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE artifacts DROP COLUMN kind`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	st, err = Open(dir, path)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{informative.Artifact.ID: TypeInformative, collaborative.Artifact.ID: TypeCollaborative} {
		art, err := st.Get(t.Context(), id)
		if err != nil || art.Type != want {
			t.Fatalf("migration %s: %+v %v", id, art, err)
		}
	}
	inbox, _, err := st.Suggestions(t.Context(), collaborative.Artifact.ID, false, 0, 50)
	if err != nil || len(inbox) != 1 || inbox[0].ID != suggestion.ID {
		t.Fatalf("feedback lost: %+v %v", inbox, err)
	}
	st.Close()
	// Migration is idempotent and never infers a new type from page views.
	st, err = Open(dir, path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	art, err := st.Get(t.Context(), informative.Artifact.ID)
	if err != nil || art.Type != TypeInformative {
		t.Fatalf("reopen: %+v %v", art, err)
	}
}
