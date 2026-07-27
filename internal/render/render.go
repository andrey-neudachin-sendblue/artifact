// Package render turns Markdown into a standalone HTML page.
//
// Rendering happens at request time, not at upload time, so an update is
// visible immediately and a template change applies to every artifact that
// already exists.
package render

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

//go:embed page.html
var files embed.FS

var page = template.Must(template.ParseFS(files, "page.html"))

// Raw HTML in Markdown is passed through. Artifact serves arbitrary .html
// files verbatim anyway, so sanitizing here would be inconsistent theatre
// rather than a security boundary; origin separation is the actual control.
var markdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(html.WithUnsafe()),
)

type pageData struct {
	Title string
	Body  template.HTML
}

// Markdown renders src and wraps it in the embedded page template.
func Markdown(w io.Writer, title string, src []byte) error {
	var body bytes.Buffer

	if err := markdown.Convert(src, &body); err != nil {
		return fmt.Errorf("convert markdown: %w", err)
	}

	if err := page.Execute(w, pageData{Title: title, Body: template.HTML(body.String())}); err != nil {
		return fmt.Errorf("execute page template: %w", err)
	}

	return nil
}
