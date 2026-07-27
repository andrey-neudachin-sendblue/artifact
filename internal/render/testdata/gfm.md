# Release plan

A paragraph with **bold**, *italic*, ~~strikethrough~~, `inline code`, and a
bare autolink: https://example.com/docs

## Checklist

- [x] Cut the release branch
- [ ] Run the smoke suite
- [ ] Publish the notes

## Coverage

| Package | Statements | Owner |
| --- | ---: | :---: |
| `store` | 94% | infra |
| `api` | 88% | infra |
| `render` | 100% | docs |

## Snippet

```go
func main() {
	fmt.Println("hello & <world>")
}
```

> A blockquote, for emphasis.

1. First
2. Second
   - Nested
   - Items

<div class="callout">Raw HTML is passed through unchanged.</div>

---

[A link](https://example.com/next) and an image: ![alt text](https://example.com/a.png)
