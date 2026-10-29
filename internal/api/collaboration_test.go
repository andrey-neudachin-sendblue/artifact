package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/10/artifact/internal/store"
)

func (h *harness) suggestionJSON(id string, version int, message string) []byte {
	h.t.Helper()
	req := h.authed("POST", "/api/artifacts/"+id+"/suggestions", strings.NewReader(fmt.Sprintf(`{"version":%d,"message":%q,"quote":"old words","location":"heading","author":"Reviewer"}`, version, message)))
	req.Header.Set("Content-Type", "application/json")
	resp, body := h.send(req)
	if resp.StatusCode != 201 {
		h.t.Fatalf("suggest: %d %s", resp.StatusCode, body)
	}
	return body
}

func TestSuggestionRevisionLifecycle(t *testing.T) {
	h := newHarness(t)
	resp, body := h.send(h.authed("POST", "/api/upload?type=collaborative&filename=report.html&slug=review-report", strings.NewReader("<h1>old words</h1>")))
	if resp.StatusCode != 201 {
		t.Fatalf("upload: %d %s", resp.StatusCode, body)
	}
	var art artifactBody
	json.Unmarshal(body, &art)
	var suggestion store.Suggestion
	json.Unmarshal(h.suggestionJSON(art.ID, 1, "Use plain language"), &suggestion)

	// The owner can fetch an inbox; an anonymous recipient cannot read it or publish.
	for _, route := range []struct{ method, path string }{
		{"GET", "suggestions"}, {"POST", fmt.Sprintf("suggestions/%d/dismiss", suggestion.ID)},
		{"POST", fmt.Sprintf("suggestions/%d/proposals", suggestion.ID)}, {"POST", "proposals/missing/accept"},
	} {
		resp, _ := h.send(h.request(route.method, "/api/artifacts/"+art.ID+"/"+route.path, nil))
		if resp.StatusCode != 401 {
			t.Fatalf("anonymous %s %s: %d", route.method, route.path, resp.StatusCode)
		}
	}
	resp, body = h.send(h.authed("GET", "/api/artifacts/review-report/suggestions", nil))
	if resp.StatusCode != 200 || !strings.Contains(string(body), art.ID+`@1/raw`) && !strings.Contains(string(body), `review-report@1/raw`) {
		t.Fatalf("inbox: %d %s", resp.StatusCode, body)
	}

	target := fmt.Sprintf("/api/artifacts/%s/suggestions/%d/proposals?filename=report.html&base_version=1", art.ID, suggestion.ID)
	resp, body = h.send(h.authed("POST", target, strings.NewReader("<h1>simple words</h1>")))
	if resp.StatusCode != 201 {
		t.Fatalf("propose: %d %s", resp.StatusCode, body)
	}
	var proposal store.Proposal
	json.Unmarshal(body, &proposal)
	resp, body = h.send(h.request("GET", "/"+art.ID, nil))
	if string(body) != "<h1>old words</h1>" {
		t.Fatalf("proposal changed live bytes: %s", body)
	}
	resp, body = h.send(h.request("GET", "/"+art.ID+"/proposals/"+proposal.ID, nil))
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Review revision") || !strings.Contains(string(body), "Use plain language") {
		t.Fatalf("preview: %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("missing review CSP")
	}
	resp, body = h.send(h.request("GET", "/"+art.ID+"/proposals/"+proposal.ID+"/content", nil))
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox allow-scripts") || !strings.Contains(string(body), "simple words") {
		t.Fatalf("candidate content is not isolated: %s", body)
	}
	resp, body = h.send(h.authed("POST", "/api/artifacts/"+art.ID+"/proposals/"+proposal.ID+"/accept", nil))
	if resp.StatusCode != 200 {
		t.Fatalf("accept: %d %s", resp.StatusCode, body)
	}
	resp, body = h.send(h.request("GET", "/"+art.ID, nil))
	if string(body) != "<h1>simple words</h1>" {
		t.Fatalf("accepted bytes: %s", body)
	}
	_, body = h.send(h.request("GET", "/"+art.ID+"@1/raw", nil))
	if string(body) != "<h1>old words</h1>" {
		t.Fatalf("v1 mutated: %s", body)
	}
	_, body = h.send(h.authed("GET", "/api/artifacts/"+art.ID+"/suggestions", nil))
	if !strings.Contains(string(body), `"suggestions":[]`) {
		t.Fatalf("resolved suggestion remains pending: %s", body)
	}
	resp, _ = h.send(h.authed("POST", "/api/artifacts/"+art.ID+"/proposals/"+proposal.ID+"/accept", nil))
	if resp.StatusCode != 409 {
		t.Fatalf("repeated accept: %d", resp.StatusCode)
	}

	// Deleting an artifact removes feedback, drafts, and their blobs.
	resp, _ = h.send(h.authed("DELETE", "/api/artifacts/"+art.ID, nil))
	if resp.StatusCode != 204 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	for _, path := range []string{"/" + art.ID + "/review", "/" + art.ID + "/proposals/" + proposal.ID, "/" + art.ID + "/proposals/" + proposal.ID + "/content"} {
		resp, _ := h.send(h.request("GET", path, nil))
		if resp.StatusCode != 404 {
			t.Fatalf("deleted %s: %d", path, resp.StatusCode)
		}
	}
	if _, err := os.Stat(filepath.Join(h.dataDir, "blobs", art.ID)); !os.IsNotExist(err) {
		t.Fatalf("deleted blobs remain: %v", err)
	}
}

func TestSuggestionsValidationAndPagination(t *testing.T) {
	h := newHarness(t)
	_, body := h.send(h.authed("POST", "/api/upload?type=collaborative&filename=plan.md", strings.NewReader("# Plan")))
	var art artifactBody
	json.Unmarshal(body, &art)
	for _, tc := range []struct {
		body, contentType, origin string
		status                    int
	}{
		{`{"message":"change"}`, "application/json", "", 400},
		{`{"version":1,"message":" "}`, "application/json", "", 400},
		{`{"version":1,"message":"x","status":"addressed"}`, "application/json", "", 400},
		{`{"version":1,"message":"x"} {}`, "application/json", "", 400},
		{`{"version":999,"message":"x"}`, "application/json", "", 404},
		{`{"version":1,"message":"x"}`, "text/plain", "", 400},
		{`{"version":1,"message":"x"}`, "application/json", "null", 403},
		{`{"version":1,"message":"x"}`, "application/json", "https://evil.example", 403},
		{fmt.Sprintf(`{"version":1,"message":%q}`, strings.Repeat("x", 8001)), "application/json", "", 400},
		{fmt.Sprintf(`{"version":1,"message":%q}`, strings.Repeat("x", 17000)), "application/json", "", 413},
		{"{\"version\":1,\"message\":\"\xff\"}", "application/json", "", 400},
	} {
		req := h.authed("POST", "/api/artifacts/"+art.ID+"/suggestions", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", tc.contentType)
		req.Header.Set("Origin", tc.origin)
		resp, body := h.send(req)
		if resp.StatusCode != tc.status {
			t.Fatalf("input %s: %d %s", tc.body[:min(len(tc.body), 80)], resp.StatusCode, body)
		}
	}
	for range 3 {
		h.suggestionJSON(art.ID, 1, "Change this")
	}
	_, body = h.send(h.authed("GET", "/api/artifacts/"+art.ID+"/suggestions?limit=2", nil))
	var page struct {
		Suggestions []store.Suggestion `json:"suggestions"`
		Next        int64              `json:"next_after"`
	}
	json.Unmarshal(body, &page)
	if len(page.Suggestions) != 2 || page.Next == 0 {
		t.Fatalf("first page: %s", body)
	}
	_, body = h.send(h.authed("GET", fmt.Sprintf("/api/artifacts/%s/suggestions?after=%d", art.ID, page.Next), nil))
	json.Unmarshal(body, &page)
	if len(page.Suggestions) != 1 || page.Next != 0 {
		t.Fatalf("last page: %s", body)
	}
	// Per-artifact rate limit persists in SQLite rather than in unbounded IP maps.
	for range 27 {
		h.suggestionJSON(art.ID, 1, "Feedback")
	}
	req := h.authed("POST", "/api/artifacts/"+art.ID+"/suggestions", strings.NewReader(`{"version":1,"message":"too many"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := h.send(req)
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("rate limit: %d", resp.StatusCode)
	}
}

func TestStaleAndConcurrentProposalAcceptance(t *testing.T) {
	h := newHarness(t)
	_, body := h.send(h.authed("POST", "/api/upload?type=collaborative&filename=plan.md", strings.NewReader("# One")))
	var art artifactBody
	json.Unmarshal(body, &art)
	var suggestion store.Suggestion
	json.Unmarshal(h.suggestionJSON(art.ID, 1, "Make it clearer"), &suggestion)
	propose := func(base int) store.Proposal {
		resp, body := h.send(h.authed("POST", fmt.Sprintf("/api/artifacts/%s/suggestions/%d/proposals?filename=plan.md&base_version=%d", art.ID, suggestion.ID, base), strings.NewReader("# Clearer")))
		if resp.StatusCode != 201 {
			t.Fatalf("propose: %d %s", resp.StatusCode, body)
		}
		var p store.Proposal
		json.Unmarshal(body, &p)
		return p
	}
	old := propose(1)
	h.send(h.authed("PUT", "/api/artifacts/"+art.ID+"?filename=plan.md", strings.NewReader("# Two")))
	resp, _ := h.send(h.authed("POST", "/api/artifacts/"+art.ID+"/proposals/"+old.ID+"/accept", nil))
	if resp.StatusCode != 409 {
		t.Fatalf("stale accepted: %d", resp.StatusCode)
	}
	// Older feedback may be addressed against the CURRENT base, after agent review.
	current := propose(2)
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wg.Go(func() {
			resp, err := http.DefaultClient.Do(h.authed("POST", "/api/artifacts/"+art.ID+"/proposals/"+current.ID+"/accept", nil))
			if err != nil {
				statuses <- 0
				return
			}
			defer resp.Body.Close()
			statuses <- resp.StatusCode
		})
	}
	wg.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatalf("concurrent accepts: %v", counts)
	}
	_, body = h.send(h.authed("GET", "/api/artifacts/"+art.ID, nil))
	if !strings.Contains(string(body), `"latest_version":3`) {
		t.Fatalf("concurrent accept created extra version: %s", body)
	}
}
