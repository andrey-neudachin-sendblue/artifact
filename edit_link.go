package main

import (
	"fmt"
)

type EditLinkCmd struct {
	ID     string `arg:"" help:"Artifact ID or slug."`
	Rotate bool   `help:"Revoke all previous edit links before issuing this one."`
}

func (e *EditLinkCmd) Run(c *cmdContext) error {
	api, err := c.api()
	if err != nil {
		return err
	}
	link, raw, err := api.EditLink(c.ctx, e.ID, e.Rotate)
	if err != nil {
		return err
	}
	if c.globals.JSON {
		return c.emitJSON(raw)
	}
	_, err = fmt.Fprintln(c.stdout, link.EditURL)
	return err
}
