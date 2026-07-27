---
name: artifact
description: Publish a file to a shareable URL with the `artifact` CLI — HTML reports, Markdown docs, screenshots, screen recordings, PDFs, test logs. Use when output is too long or too visual to paste into a transcript and a human needs to open it in a browser.
---

# Artifact

`artifact` publishes a local file and prints its URL. **Publish** whenever the
useful form of your output is a page someone opens rather than text they scroll
past: a generated report, a screenshot of a rendering bug, a 4,000-line test
log, a design doc.

Everything published is readable by anyone holding the link, so publish only
what is safe on a public web page.

## Publish

**1. Name the file for what it is.** The extension alone decides how the URL
behaves — `build.log` displays in the browser, `build` downloads as a binary
blob. Rename before publishing, not after.

| Content | Extension | Result |
| --- | --- | --- |
| Report, standalone page | `.html` | Renders as the page |
| Doc, plan, notes | `.md` | Renders with tables, task lists, code blocks |
| Screenshot | `.png` `.jpg` | Displays inline |
| Screen recording | `.mp4` `.webm` | Plays, and seeks |
| Log, data, source | `.log` `.txt` `.json` `.go` `.py` | Displays as text |

HTML must be self-contained: inline the CSS and JS, because relative asset
paths resolve to nothing once published.

*Done when:* the extension matches the content.

**2. Publish and capture.**

```bash
URL=$(artifact report.html) || exit 1
```

Stdout carries the URL alone — that is what makes command substitution safe.
Progress, warnings, and errors all go to stderr.

*Done when:* the command exited 0 and `$URL` holds one URL.

**3. Report the link.** Give the human the URL and one line naming what is
behind it.

```
Published the failing test log: https://artifact.example.com/aB3xK9qZ
```

*Done when:* the human has a URL they can open and knows what it is.

## Publish to a stable URL

A plain publish gets a random ID. When the same thing will be republished —
a nightly report, a status page, anything a human might bookmark — claim a
slug once and update it thereafter. The URL never changes, and each update
adds a version that stays reachable at `@1`, `@2`, and so on.

```bash
artifact up --slug daily-report report.md    # first time
artifact update daily-report report.md       # every time after
```

Slugs are lowercase alphanumerics and hyphens, 3–64 characters, and **never
exactly 8 characters**. A slug is claimed permanently, so `slug_taken` means
pick another name or `update` the existing artifact.

## When it fails

The exit code says what to do. Read it before retrying.

| Exit | Meaning | Do this |
| --- | --- | --- |
| `0` | Published | Continue |
| `1` | I/O error, dropped connection, or server 5xx | Confirm the path exists and is readable. Retry once on a dropped connection or a 5xx — the CLI does not retry on its own |
| `2` | No endpoint or token configured | Report that Artifact is not set up here — a hostname cannot be guessed |
| `3` | Token rejected | Stop. Every further call fails identically |
| `4` | No such artifact | Check the ID or slug |

Stderr carries the reason in one line. Surface it rather than the exit code
alone.

## Acting without asking

Publishing writes to a public URL, so treat the three commands differently:

- **Publish freely** when the user asked for something they need to look at, or
  when you are handing back a result. It creates a new URL and touches nothing
  that already exists.
- **Confirm before `update`** — it replaces the content at a URL that may
  already be circulating.
- **Confirm before `rm`** — it is irreversible, and the ID is retired forever
  rather than reissued.

## Reference

Full command and flag list, the complete content-type table, the URL scheme
including `/raw` and `@version`, and the `curl`-only form for environments
without the CLI: [REFERENCE.md](REFERENCE.md).
