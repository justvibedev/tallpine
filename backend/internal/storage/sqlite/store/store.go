// Package store contains SQLite-backed table stores built on sqlc-generated
// queries.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"time"

	"tallpine.eu/tallpine/backend/internal/managedcfg"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// Store is the SQLite-backed persistence layer. It routes writes to a single
// writer connection (qw) and reads to a reader pool (qr) — see Open. writeMu
// guards the read-modify-write write methods (e.g. CreateSession's
// next-num-then-insert) so concurrent writes can't interleave them.
//
// CDC is captured by DB triggers (migration 0001), NOT by this layer: the store
// never writes change_log, it only reads it for the CDC poller.
type Store struct {
	writeDB *sql.DB
	readDB  *sql.DB
	qw      *gen.Queries // bound to the single writer connection
	qr      *gen.Queries // bound to the reader pool
	writeMu *contextMutex
	// managed locks project settings (B212): reads overlay them, writes keep the
	// stored value. Set once at startup, before the store is shared.
	managed *managedcfg.Locks
	// envCodec seals project env values at rest (hardened profile). Set once.
	envCodec *EnvCodec
	// coalesceActivityWrites mirrors the storage-compaction setting. When set,
	// an activity upsert that changes nothing is not written at all: no row
	// rewrite, no revision bump, no change_log row.
	coalesceActivityWrites atomic.Bool
	// projections group-commits concurrent Chat provider-event projections.
	projections projectionBatcher
	// conversionVacuum wraps the auto-vacuum conversion's VACUUM in tests,
	// which cannot otherwise land a cancellation after the commit. Nil runs it
	// directly.
	conversionVacuum func(ctx context.Context, exec func(context.Context) error) error
}

// WriterContention counts writes that had to wait since the store opened,
// either for the writer lock or, for single statements that do not take it,
// for the one writer connection. A background task compares it across its own
// work to tell whether it is delaying foreground writes.
func (s *Store) WriterContention() uint64 {
	return s.writeMu.contended.Load() + uint64(s.writeDB.Stats().WaitCount) // #nosec G115 -- WaitCount is a non-negative counter
}

// WriterHold describes one background batch's use of the writer lock.
type WriterHold struct {
	// Held is how long the batch held the lock, excluding its wait for it.
	Held time.Duration
	// Contended reports that another write queued for the writer meanwhile.
	Contended bool
}

// holdWriter takes the writer lock for a background batch. The returned
// release unlocks it and reports how long it was held and whether another
// write waited for it in that time.
func (s *Store) holdWriter(ctx context.Context) (func() WriterHold, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return nil, err
	}
	started, base := time.Now(), s.WriterContention()
	return func() WriterHold {
		hold := WriterHold{Held: time.Since(started), Contended: s.WriterContention() != base}
		s.writeMu.Unlock()
		return hold
	}, nil
}

// WriterAcquisitions counts writer-lock acquisitions since the store opened:
// a cheap measure of how busy the daemon's write path is.
func (s *Store) WriterAcquisitions() uint64 { return s.writeMu.acquired.Load() }

// SetCoalesceWrites applies the storage-compaction setting to the write path
// without persisting it. On by default, like the setting itself.
func (s *Store) SetCoalesceWrites(enabled bool) { s.coalesceActivityWrites.Store(enabled) }

// CoalescesWrites reports whether the write path coalesces unchanged writes.
func (s *Store) CoalescesWrites() bool { return s.coalesceActivityWrites.Load() }

type contextMutex struct {
	token chan struct{}
	// acquired counts every acquisition; contended counts those that found
	// the lock held and had to wait. Background work reads them to tell a
	// quiet daemon from a busy one and to notice it is in another write's way.
	acquired  atomic.Uint64
	contended atomic.Uint64
}

func newContextMutex() *contextMutex {
	mutex := &contextMutex{token: make(chan struct{}, 1)}
	mutex.token <- struct{}{}
	return mutex
}

func (m *contextMutex) Lock() {
	m.acquired.Add(1)
	select {
	case <-m.token:
		return
	default:
	}
	m.contended.Add(1)
	<-m.token
}

func (m *contextMutex) LockContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.acquired.Add(1)
	select {
	case <-m.token:
		return nil
	default:
	}
	m.contended.Add(1)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.token:
		if err := ctx.Err(); err != nil {
			m.token <- struct{}{}
			return err
		}
		return nil
	}
}

func (m *contextMutex) Unlock() {
	m.token <- struct{}{}
}

type conversationProjectionTxKey struct{}

// conversationWriter returns the transaction-bound query set when a provider
// event projection is in progress. Otherwise it acquires the ordinary single
// writer lock. This makes the existing focused store methods composable inside
// one archive+projection transaction without exposing SQLite transactions above
// the adapter boundary.
func (s *Store) conversationWriter(ctx context.Context) (*gen.Queries, func()) {
	if q, ok := ctx.Value(conversationProjectionTxKey{}).(*gen.Queries); ok && q != nil {
		return q, func() {}
	}
	s.writeMu.Lock()
	return s.qw, s.writeMu.Unlock
}

func (s *Store) conversationReader(ctx context.Context) *gen.Queries {
	if q, ok := ctx.Value(conversationProjectionTxKey{}).(*gen.Queries); ok && q != nil {
		return q
	}
	return s.qr
}

// NewStore wraps an opened writer + reader *sql.DB (see Open) as a Store.
func NewStore(writeDB, readDB *sql.DB) *Store {
	s := &Store{
		writeDB: writeDB,
		readDB:  readDB,
		qw:      gen.New(writeDB),
		qr:      gen.New(readDB),
		writeMu: newContextMutex(),
	}
	s.coalesceActivityWrites.Store(true)
	return s
}

// Close closes both pools.
func (s *Store) Close() error {
	err := s.writeDB.Close()
	if e := s.readDB.Close(); e != nil && err == nil {
		err = e
	}
	return err
}

// inTx runs fn inside a single write transaction on the writer connection,
// rolling back on error. The caller must already hold writeMu.
func (s *Store) inTx(ctx context.Context, what string, fn func(*gen.Queries) error) error {
	return s.inTxDB(ctx, what, func(q *gen.Queries, _ gen.DBTX) error { return fn(q) })
}

// inTxDB is the raw-database variant used by handwritten transactional stores
// while their sqlc artifacts are intentionally regenerated in a later slice.
func (s *Store) inTxDB(ctx context.Context, what string, fn func(*gen.Queries, gen.DBTX) error) error {
	if q, ok := ctx.Value(conversationProjectionTxKey{}).(*gen.Queries); ok && q != nil {
		if err := fn(q, nil); err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		return nil
	}
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin %s: %w", what, err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(s.qw.WithTx(tx), tx); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return tx.Commit()
}
