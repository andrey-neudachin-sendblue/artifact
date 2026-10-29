package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestEditCapabilityScopeAndConflicts(t *testing.T) {
	h := newHarness(t)
	var ids, keys []string
	for range 2 {
		_, body := h.send(h.authed("POST", "/api/upload?filename=plan.md", strings.NewReader("# Original")))
		var art struct {
			ID      string `json:"id"`
			EditURL string `json:"edit_url"`
		}
		if err := json.Unmarshal(body, &art); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, art.ID)
		keys = append(keys, strings.Split(art.EditURL, "#key=")[1])
	}
	for _, tc := range []struct {
		method, path, key string
		status            int
	}{
		{"GET", "/api/artifacts/" + ids[0], keys[0], 200},
		{"GET", "/api/artifacts/" + ids[1], keys[0], 401},
		{"GET", "/api/artifacts/" + ids[0], strings.Repeat("0", 64), 401},
		{"PUT", "/api/artifacts/" + ids[0] + "?filename=plan.md", keys[0], 400},
		{"PUT", "/api/artifacts/" + ids[0] + "?filename=plan.md&base_version=1", keys[1], 401},
		{"PUT", "/api/artifacts/" + ids[0] + "?filename=plan.md&base_version=1", keys[0], 200},
		{"PUT", "/api/artifacts/" + ids[0] + "?filename=plan.md&base_version=1", keys[0], 409},
		{"DELETE", "/api/artifacts/" + ids[0], keys[0], 401},
		{"POST", "/api/artifacts/" + ids[0] + "/edit-link", keys[0], 401},
		{"POST", "/api/upload?filename=plan.md", keys[0], 401},
		{"GET", "/api/artifacts", keys[0], 401},
	} {
		req := h.request(tc.method, tc.path, strings.NewReader("# Changed"))
		req.Header.Set("Authorization", "Bearer "+tc.key)
		resp, body := h.send(req)
		if resp.StatusCode != tc.status {
			t.Fatalf("%s %s: %d want %d %s", tc.method, tc.path, resp.StatusCode, tc.status, body)
		}
	}
	_, old := h.send(h.request("GET", "/"+ids[0]+"@1/raw", nil))
	if string(old) != "# Original" {
		t.Fatal("old version changed")
	}
	_, body := h.send(h.authed("POST", "/api/artifacts/"+ids[0]+"/edit-link?rotate=true", nil))
	var links struct {
		EditURL string `json:"edit_url"`
	}
	json.Unmarshal(body, &links)
	req := h.request("GET", "/api/artifacts/"+ids[0], nil)
	req.Header.Set("Authorization", "Bearer "+keys[0])
	resp, _ := h.send(req)
	if resp.StatusCode != 401 {
		t.Fatal("revoked key accepted")
	}
	req = h.request("GET", "/api/artifacts/"+ids[0], nil)
	req.Header.Set("Authorization", "Bearer "+strings.Split(links.EditURL, "#key=")[1])
	resp, _ = h.send(req)
	if resp.StatusCode != 200 {
		t.Fatal("new key rejected")
	}
}

func TestEditKeyCanCompleteRevisionWorkflow(t *testing.T) {
	h := newHarness(t)
	_, body := h.send(h.authed("POST", "/api/upload?filename=plan.md", strings.NewReader("# First")))
	var art struct {
		ID      string `json:"id"`
		EditURL string `json:"edit_url"`
	}
	json.Unmarshal(body, &art)
	key := strings.Split(art.EditURL, "#key=")[1]
	send := func(method, path, body string) []byte {
		t.Helper()
		req := h.request(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+key)
		if strings.HasSuffix(path, "/suggestions") && method == "POST" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, out := h.send(req)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			t.Fatalf("key workflow %s: %d %s", path, resp.StatusCode, out)
		}
		return out
	}
	base := "/api/artifacts/" + art.ID
	var suggestion struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(send("POST", base+"/suggestions", `{"version":1,"message":"Improve this"}`), &suggestion)
	if !strings.Contains(string(send("GET", base+"/suggestions", "")), "Improve this") {
		t.Fatal("key cannot read inbox")
	}
	var proposal struct {
		ID string `json:"id"`
	}
	json.Unmarshal(send("POST", base+"/suggestions/"+fmt.Sprint(suggestion.ID)+"/proposals?filename=plan.md&base_version=1", "# Improved"), &proposal)
	send("POST", base+"/proposals/"+proposal.ID+"/accept", "")
	_, raw := h.send(h.request("GET", "/"+art.ID+"/raw", nil))
	if string(raw) != "# Improved" {
		t.Fatal("key revision did not publish")
	}
	_, raw = h.send(h.request("GET", "/"+art.ID+"@1/raw", nil))
	if string(raw) != "# First" {
		t.Fatal("key revision changed history")
	}
}
