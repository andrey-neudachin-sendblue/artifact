package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollaborationSurvivesRestartAndDismissal(t *testing.T) {
	dir := t.TempDir()
	open := func() *Store {
		st, err := Open(dir, filepath.Join(dir, "artifact.db"))
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	st := open()
	art, err := st.Create(t.Context(), CreateInput{Type: TypeCollaborative, Filename: "plan.md", Slug: "durable-plan", Body: strings.NewReader("# One")})
	if err != nil {
		t.Fatal(err)
	}
	suggestion, err := st.AddSuggestion(t.Context(), art.Artifact.ID, Suggestion{Version: 1, Message: "Clarify it"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.Propose(t.Context(), "durable-plan", suggestion.ID, 1, "plan.md", strings.NewReader("# Clearer"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st = open()
	defer st.Close()
	inbox, _, err := st.Suggestions(t.Context(), "durable-plan", false, 0, 50)
	if err != nil || len(inbox) != 1 || inbox[0].Message != "Clarify it" {
		t.Fatalf("inbox after restart: %+v %v", inbox, err)
	}
	_, restored, err := st.Proposal(t.Context(), "durable-plan", p.ID)
	if err != nil || restored.SHA256 != p.SHA256 {
		t.Fatalf("proposal after restart: %+v %v", restored, err)
	}
	body, err := os.ReadFile(st.ProposalPath(art.Artifact.ID, p.ID))
	if err != nil || string(body) != "# Clearer" {
		t.Fatalf("proposal bytes after restart: %q %v", body, err)
	}
	// Dismissing a suggestion invalidates its existing candidate, including after restart.
	if err := st.DismissSuggestion(t.Context(), "durable-plan", suggestion.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AcceptProposal(t.Context(), "durable-plan", p.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("dismissed proposal acceptance: %v", err)
	}
	if _, err := st.Propose(t.Context(), "durable-plan", suggestion.ID, 1, "plan.md", strings.NewReader("# Again")); !errors.Is(err, ErrConflict) {
		t.Fatalf("dismissed proposal creation: %v", err)
	}
	current, err := st.Get(t.Context(), "durable-plan")
	if err != nil || current.LatestVersion != 1 {
		t.Fatalf("dismissal changed published version: %+v %v", current, err)
	}
	assertNoTempFiles(t, st.artifactDir(art.Artifact.ID))
}
