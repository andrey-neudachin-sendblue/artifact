package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

// IssueEditKey returns a new capability once; only its digest is persisted.
// Rotation revokes every previously issued key for this artifact.
func (s *Store) IssueEditKey(ctx context.Context, ref string, rotate bool) (string, error) {
	art, err := s.Get(ctx, ref)
	if err != nil {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	key := hex.EncodeToString(raw)
	digest := sha256.Sum256([]byte(key))
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if rotate {
		if _, err := tx.ExecContext(ctx, `DELETE FROM artifact_edit_keys WHERE artifact_id = ?`, art.ID); err != nil {
			return "", err
		}
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO artifact_edit_keys (artifact_id, digest)
 SELECT id, ? FROM artifacts WHERE id = ? AND deleted_at IS NULL`, hex.EncodeToString(digest[:]), art.ID)
	if err != nil {
		return "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return key, nil
}

func (s *Store) CheckEditKey(ctx context.Context, ref, key string) (bool, error) {
	if len(key) != 64 {
		return false, nil
	}
	art, err := s.Get(ctx, ref)
	if err != nil {
		return false, err
	}
	digest := sha256.Sum256([]byte(key))
	var n int
	err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM artifact_edit_keys WHERE artifact_id = ? AND digest = ?`, art.ID, hex.EncodeToString(digest[:])).Scan(&n)
	return n == 1, err
}
