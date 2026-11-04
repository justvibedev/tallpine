package store

import (
	"context"
	"database/sql"
	"time"
)

// RawProjectConfig returns the stored config JSON of project id, as on disk.
func (s *Store) RawProjectConfig(ctx context.Context, id string) (string, error) {
	var cfg sql.NullString
	err := s.readDB.QueryRowContext(ctx, `SELECT config FROM projects WHERE id = ?`, id).Scan(&cfg)
	return cfg.String, err
}

// SetRawProjectConfig overwrites the stored config JSON of project id.
func (s *Store) SetRawProjectConfig(ctx context.Context, id, config string) error {
	_, err := s.writeDB.ExecContext(ctx, `UPDATE projects SET config = ? WHERE id = ?`, config, id)
	return err
}

// SetEnvCodecClock swaps the codec's clock (key-failure backoff tests).
func (c *EnvCodec) SetEnvCodecClock(now func() time.Time) { c.now = now }
