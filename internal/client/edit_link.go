package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

type EditLink struct {
	ViewURL string `json:"view_url"`
	EditURL string `json:"edit_url"`
}

func (c *Client) EditLink(ctx context.Context, id string, rotate bool) (*EditLink, []byte, error) {
	req, err := c.request(ctx, http.MethodPost, "/api/artifacts/"+url.PathEscape(id)+"/edit-link?rotate="+strconv.FormatBool(rotate), nil)
	if err != nil {
		return nil, nil, err
	}
	return decode[EditLink](c.do(req))
}
