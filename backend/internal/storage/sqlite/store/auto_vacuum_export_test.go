package store

import "context"

// SetConversionVacuumForTest wraps the auto-vacuum conversion's VACUUM, so a
// test can make it fail before its commit or report cancellation after it.
func (s *Store) SetConversionVacuumForTest(wrap func(ctx context.Context, exec func(context.Context) error) error) {
	s.conversionVacuum = wrap
}

// WriterAutocheckpointForTest reads wal_autocheckpoint on the writer
// connection, which the conversion pauses and must always restore.
func (s *Store) WriterAutocheckpointForTest(ctx context.Context) (int64, error) {
	var pages int64
	err := s.writeDB.QueryRowContext(ctx, "PRAGMA wal_autocheckpoint").Scan(&pages)
	return pages, err
}
