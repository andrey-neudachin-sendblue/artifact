package api_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEveryArtifactSupportsKeyedEditing(t *testing.T) {
	h := newHarness(t)
	for _, kind := range []string{"informative", "collaborative"} {
		_, body := h.send(h.authed("POST", "/api/upload?filename=report.html&type="+kind, strings.NewReader("<h1>Original</h1>")))
		var art struct {
			ID      string `json:"id"`
			EditURL string `json:"edit_url"`
		}
		json.Unmarshal(body, &art)
		key := strings.Split(art.EditURL, "#key=")[1]
		for _, path := range []string{"/" + art.ID, "/" + art.ID + "/edit", "/" + art.ID + "/review", "/" + art.ID + "/review/instructions"} {
			resp, body := h.send(h.request("GET", path, nil))
			if resp.StatusCode != 200 || strings.Contains(string(body), key) {
				t.Fatalf("view leaks access: %s %d", path, resp.StatusCode)
			}
		}
		req := h.request("POST", "/api/artifacts/"+art.ID+"/suggestions", strings.NewReader(`{"version":1,"message":"Change"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := h.send(req)
		if resp.StatusCode != 401 {
			t.Fatalf("view-only write: %d", resp.StatusCode)
		}
		req = h.request("POST", "/api/artifacts/"+art.ID+"/suggestions", strings.NewReader(`{"version":1,"message":"Change"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+key)
		resp, body = h.send(req)
		if resp.StatusCode != 201 {
			t.Fatalf("key suggestion: %d %s", resp.StatusCode, body)
		}
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
