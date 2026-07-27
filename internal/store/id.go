package store

import (
	"crypto/rand"
	"fmt"
	"regexp"
)

// IDLength is the fixed width of a generated artifact ID. Slugs are forbidden
// from being exactly this long, which is what keeps the two namespaces
// disjoint and root-level resolution unambiguous.
const IDLength = 8

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// slugPattern is the format from the requirements: lowercase alphanumerics and
// interior hyphens.
var slugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// reservedSlugs can never be taken, so a slug cannot shadow a route.
var reservedSlugs = map[string]bool{
	"api":         true,
	"healthz":     true,
	"raw":         true,
	"static":      true,
	"favicon.ico": true,
	"robots.txt":  true,
}

// newID returns IDLength random base62 characters. Bytes at or above 248 are
// rejected rather than folded, so every character is uniformly distributed.
func newID() (string, error) {
	out := make([]byte, 0, IDLength)
	buf := make([]byte, IDLength)

	for len(out) < IDLength {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("read random bytes: %w", err)
		}

		for _, b := range buf {
			if b >= 248 {
				continue
			}

			out = append(out, base62[b%62])
			if len(out) == IDLength {
				break
			}
		}
	}

	return string(out), nil
}

// ValidateSlug enforces the format, the length rules, and the reserved list.
func ValidateSlug(slug string) error {
	switch {
	case len(slug) < 3 || len(slug) > 64:
		return fmt.Errorf("%w: must be 3-64 characters", ErrBadSlug)
	case len(slug) == IDLength:
		return fmt.Errorf("%w: must not be exactly %d characters, which is reserved for generated IDs", ErrBadSlug, IDLength)
	case !slugPattern.MatchString(slug):
		return fmt.Errorf("%w: must match [a-z0-9]([a-z0-9-]*[a-z0-9])?", ErrBadSlug)
	case reservedSlugs[slug]:
		return fmt.Errorf("%w: %q is reserved", ErrBadSlug, slug)
	}

	return nil
}
