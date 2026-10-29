package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestCollaborationCLI(t *testing.T) {
	c := newCLI(t)
	code, out, errout := c.run(c.connected("up", "--slug", "cli-review", "--review", c.writeFile("plan.md", "# Plan"))...)
	if code != 0 || strings.TrimSpace(out) != "https://artifact.example.com/cli-review/review" {
		t.Fatalf("review upload: %d %s %s", code, out, errout)
	}
	code, out, errout = c.run(c.connected("review", "cli-review", "--version", "1")...)
	if code != 0 || !strings.Contains(out, "cli-review@1/review") {
		t.Fatalf("review link: %d %s %s", code, out, errout)
	}
	req, _ := http.NewRequest("POST", c.server.URL+"/api/artifacts/cli-review/suggestions", strings.NewReader(`{"version":1,"message":"Clarify the plan"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("suggest: %d", resp.StatusCode)
	}
	var s struct {
		ID int64 `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&s)
	code, out, errout = c.run(c.connected("--json", "suggestions", "cli-review")...)
	if code != 0 || !strings.Contains(out, "Clarify the plan") || !strings.Contains(out, "cli-review@1/raw") {
		t.Fatalf("inbox: %d %s %s", code, out, errout)
	}
	code, out, errout = c.run(c.connected("--json", "propose", "cli-review", fmt.Sprint(s.ID), c.writeFile("plan.md", "# Clear plan"), "--base-version", "1")...)
	if code != 0 {
		t.Fatalf("propose: %d %s %s", code, out, errout)
	}
	var p struct {
		ID         string `json:"id"`
		PreviewURL string `json:"preview_url"`
	}
	json.Unmarshal([]byte(out), &p)
	if p.ID == "" || !strings.Contains(p.PreviewURL, "/proposals/") {
		t.Fatalf("proposal output: %s", out)
	}
	code, out, errout = c.run(c.connected("accept", "cli-review", p.ID)...)
	if code != 0 || strings.TrimSpace(out) != "https://artifact.example.com/cli-review" {
		t.Fatalf("accept: %d %s %s", code, out, errout)
	}
	code, out, errout = c.run(c.connected("--json", "suggestions", "cli-review", "--all")...)
	if code != 0 || !strings.Contains(out, `"status":"addressed"`) {
		t.Fatalf("addressed: %d %s %s", code, out, errout)
	}
	code, _, _ = c.run(c.connected("accept", "cli-review", p.ID)...)
	if code != 1 {
		t.Fatalf("repeated accept exit: %d", code)
	}
}
