package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestEditKeysPersistAndRotate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact.db")
	st, err := Open(dir, path)
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.Create(t.Context(), CreateInput{Filename: "a.md", Body: strings.NewReader("# A")})
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.Create(t.Context(), CreateInput{Filename: "b.md", Body: strings.NewReader("# B")})
	if err != nil {
		t.Fatal(err)
	}
	key, err := st.IssueEditKey(t.Context(), a.Artifact.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	var digest string
	if err := st.db.QueryRow(`SELECT digest FROM artifact_edit_keys WHERE artifact_id = ?`, a.Artifact.ID).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	if digest == key || len(digest) != 64 {
		t.Fatal("key must be stored only as a digest")
	}
	st.Close()
	st, err = Open(dir, path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, tc := range []struct {
		id, key string
		want    bool
	}{{a.Artifact.ID, key, true}, {b.Artifact.ID, key, false}, {a.Artifact.ID, "", false}} {
		ok, err := st.CheckEditKey(t.Context(), tc.id, tc.key)
		if err != nil || ok != tc.want {
			t.Fatalf("scope/persistence: %t %v", ok, err)
		}
	}
	second, err := st.IssueEditKey(t.Context(), a.Artifact.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := st.IssueEditKey(t.Context(), a.Artifact.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range []string{key, second} {
		if ok, _ := st.CheckEditKey(t.Context(), a.Artifact.ID, old); ok {
			t.Fatal("rotation retained old key")
		}
	}
	if ok, _ := st.CheckEditKey(t.Context(), a.Artifact.ID, rotated); !ok {
		t.Fatal("new key rejected")
	}
	if err := st.Delete(t.Context(), a.Artifact.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.CheckEditKey(t.Context(), a.Artifact.ID, rotated); ok {
		t.Fatal("deleted artifact key accepted")
	}
}
