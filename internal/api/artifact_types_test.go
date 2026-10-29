package api_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInformativeArtifactHasNoCollaboration(t *testing.T) {
	h := newHarness(t)
	original := "<h1>Informative report</h1>"
	resp, body := h.send(h.authed("POST", "/api/upload?filename=report.html", strings.NewReader(original)))
	if resp.StatusCode != 201 {
		t.Fatalf("upload: %d %s", resp.StatusCode, body)
	}
	var art struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		ReviewURL string `json:"review_url"`
	}
	json.Unmarshal(body, &art)
	if art.Type != "informative" || art.ReviewURL != "" || strings.Contains(string(body), "review_url") {
		t.Fatalf("default artifact: %s", body)
	}
	for _, path := range []string{"/" + art.ID, "/" + art.ID + "@1/raw"} {
		resp, body := h.send(h.request("GET", path, nil))
		if resp.StatusCode != 200 || string(body) != original || resp.Header.Get("Link") != "" {
			t.Fatalf("informative bytes/metadata: %d %s %v", resp.StatusCode, body, resp.Header)
		}
	}
	for _, path := range []string{"/review", "/review/content", "/review/instructions", "/proposals/missing", "/proposals/missing/content", "/proposals/missing/instructions"} {
		resp, _ := h.send(h.request("GET", "/"+art.ID+path, nil))
		if resp.StatusCode != 404 {
			t.Fatalf("informative %s: %d", path, resp.StatusCode)
		}
	}
	req := h.request("POST", "/api/artifacts/"+art.ID+"/suggestions", strings.NewReader(`{"version":1,"message":"Enable feedback"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, body = h.send(req)
	if resp.StatusCode != 409 || !strings.Contains(string(body), "collaboration_disabled") {
		t.Fatalf("informative feedback: %d %s", resp.StatusCode, body)
	}
	for _, op := range []struct{ method, path string }{
		{"GET", "suggestions"}, {"POST", "suggestions/1/dismiss"}, {"POST", "suggestions/1/proposals?filename=report.html&base_version=1"}, {"POST", "proposals/missing/accept"},
	} {
		resp, body := h.send(h.authed(op.method, "/api/artifacts/"+art.ID+"/"+op.path, strings.NewReader("<h1>Candidate</h1>")))
		if resp.StatusCode != 409 {
			t.Fatalf("informative %s: %d %s", op.path, resp.StatusCode, body)
		}
	}
	// Updates retain the type; trying to change it is rejected explicitly.
	resp, body = h.send(h.authed("PUT", "/api/artifacts/"+art.ID+"?filename=report.html&type=collaborative", strings.NewReader("changed")))
	if resp.StatusCode != 400 {
		t.Fatalf("type conversion: %d %s", resp.StatusCode, body)
	}
	resp, body = h.send(h.authed("PUT", "/api/artifacts/"+art.ID+"?filename=report.html", strings.NewReader("changed")))
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"type":"informative"`) || strings.Contains(string(body), "review_url") {
		t.Fatalf("informative update: %d %s", resp.StatusCode, body)
	}
	_, body = h.send(h.authed("GET", "/api/artifacts/"+art.ID, nil))
	if !strings.Contains(string(body), `"type":"informative"`) || strings.Contains(string(body), "review_url") {
		t.Fatalf("informative info: %s", body)
	}
}

func TestArtifactTypeValidationAndCollaborativeUpdates(t *testing.T) {
	h := newHarness(t)
	for _, kind := range []string{"typo", "Collaborative"} {
		resp, body := h.send(h.authed("POST", "/api/upload?filename=plan.md&type="+kind, strings.NewReader("# Plan")))
		if resp.StatusCode != 400 {
			t.Fatalf("invalid type %q: %d %s", kind, resp.StatusCode, body)
		}
	}
	resp, body := h.send(h.authed("POST", "/api/upload?filename=plan.md&type=collaborative", strings.NewReader("# Plan")))
	var art struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		ReviewURL string `json:"review_url"`
	}
	json.Unmarshal(body, &art)
	if resp.StatusCode != 201 || art.Type != "collaborative" || art.ReviewURL == "" {
		t.Fatalf("explicit collaborative: %d %s", resp.StatusCode, body)
	}
	resp, body = h.send(h.authed("PUT", "/api/artifacts/"+art.ID+"?filename=plan.md", strings.NewReader("# Revised")))
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"type":"collaborative"`) {
		t.Fatalf("collaborative update: %d %s", resp.StatusCode, body)
	}
	_, body = h.send(h.authed("GET", "/api/artifacts", nil))
	if !strings.Contains(string(body), `"type":"collaborative"`) {
		t.Fatalf("list missing type: %s", body)
	}
}
