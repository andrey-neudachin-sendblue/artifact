package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

type Suggestion struct {
	ID         int64  `json:"id"`
	ArtifactID string `json:"artifact_id"`
	Version    int    `json:"version"`
	Message    string `json:"message"`
	Author     string `json:"author"`
	Quote      string `json:"quote"`
	Location   string `json:"location"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
	VersionURL string `json:"version_url"`
	RawURL     string `json:"raw_url"`
}

type Suggestions struct {
	Suggestions   []Suggestion `json:"suggestions"`
	NextAfter     int64        `json:"next_after"`
	LatestVersion int          `json:"latest_version"`
}

type Proposal struct {
	ID           string `json:"id"`
	ArtifactID   string `json:"artifact_id"`
	SuggestionID int64  `json:"suggestion_id"`
	BaseVersion  int    `json:"base_version"`
	Filename     string `json:"filename"`
	ContentType  string `json:"content_type"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`
	PreviewURL   string `json:"preview_url"`
}

func (c *Client) Suggestions(ctx context.Context, id string, all bool, after int64, limit int) (*Suggestions, []byte, error) {
	q := url.Values{"all": {strconv.FormatBool(all)}, "after": {strconv.FormatInt(after, 10)}, "limit": {strconv.Itoa(limit)}}
	req, err := c.request(ctx, http.MethodGet, "/api/artifacts/"+url.PathEscape(id)+"/suggestions?"+q.Encode(), nil)
	if err != nil {
		return nil, nil, err
	}
	return decode[Suggestions](c.do(req))
}

func (c *Client) Propose(ctx context.Context, id string, suggestion int64, base int, path, filename string) (*Proposal, []byte, error) {
	q := url.Values{"base_version": {strconv.Itoa(base)}, "filename": {filename}}
	target := "/api/artifacts/" + url.PathEscape(id) + "/suggestions/" + strconv.FormatInt(suggestion, 10) + "/proposals?" + q.Encode()
	return sendFile[Proposal](ctx, c, http.MethodPost, target, path)
}

func (c *Client) Accept(ctx context.Context, id, proposal string) (*Artifact, []byte, error) {
	req, err := c.request(ctx, http.MethodPost, "/api/artifacts/"+url.PathEscape(id)+"/proposals/"+url.PathEscape(proposal)+"/accept", nil)
	if err != nil {
		return nil, nil, err
	}
	return decode[Artifact](c.do(req))
}

func (c *Client) Dismiss(ctx context.Context, id string, suggestion int64) error {
	req, err := c.request(ctx, http.MethodPost, "/api/artifacts/"+url.PathEscape(id)+"/suggestions/"+strconv.FormatInt(suggestion, 10)+"/dismiss", nil)
	if err != nil {
		return err
	}
	_, err = c.do(req)
	return err
}
