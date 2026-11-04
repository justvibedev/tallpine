package store

import "context"

// ExecForTest runs a statement on the writer, for fixtures no store API writes.
func (s *Store) ExecForTest(ctx context.Context, query string, args ...any) error {
	_, err := s.writeDB.ExecContext(ctx, query, args...)
	return err
}

// CountForTest runs a single-integer query on the writer.
func (s *Store) CountForTest(ctx context.Context, query string, args ...any) (int64, error) {
	var n int64
	err := s.writeDB.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}
