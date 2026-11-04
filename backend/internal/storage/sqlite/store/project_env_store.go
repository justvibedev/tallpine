package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// SetEnvCodec installs the codec that seals project env values at rest (managed
// hardened profile). Call it once, before the store is shared. Without one,
// env is stored as plaintext and a sealed value fails closed on read.
func (s *Store) SetEnvCodec(c *EnvCodec) {
	if c != nil {
		c.anySealed = s.anySealedEnv
	}
	s.envCodec = c
}

// primeEnvKey reads the env key before a write takes writeMu and returns the
// context to use under it. The first read can block on a Keychain prompt, and
// doing that under the store lock freezes every other write (session spawns
// included). The returned context makes the codec use only a key already
// loaded, so a write under the lock never waits on the Keychain; a failed
// prime shows up there as the codec's cached failure or ErrProjectEnvLocked.
// The key is primed when sealing is on or the stored row of id holds sealed
// values (sealing off after a hardened run).
func (s *Store) primeEnvKey(ctx context.Context, id string) context.Context {
	if s.envCodec == nil {
		return ctx
	}
	need := s.envCodec.sealing()
	if !need {
		if p, err := s.qr.GetProject(ctx, domain.ProjectID(id)); err == nil {
			need = hasSealed(unmarshalProjectConfig(p.Config).Env)
		}
	}
	if need {
		_, _ = s.envCodec.cipherFor(ctx)
	}
	return cachedKeyOnly(ctx)
}

// encodeProjectConfig is marshalProjectConfig with the env sealed for project
// id. When the stored row already holds sealed values the key must be readable
// even with sealing off, so a write never replaces env it could not read (for
// example a list row whose env was dropped) with plaintext. Call it with the
// write lock held, on the queries of the write.
func (s *Store) encodeProjectConfig(ctx context.Context, q *gen.Queries, id string, cfg domain.ProjectConfig) (sql.NullString, error) {
	if err := s.requireKeyForSealedRow(ctx, q, id); err != nil {
		return sql.NullString{}, err
	}
	sealed, err := s.envCodec.Seal(ctx, id, cfg.Env)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("write project %s: %w", id, err)
	}
	cfg.Env = sealed
	return marshalProjectConfig(cfg)
}

// requireKeyForSealedRow fails with ports.ErrProjectEnvLocked when the stored
// row of id holds sealed values and the key cannot be read.
func (s *Store) requireKeyForSealedRow(ctx context.Context, q *gen.Queries, id string) error {
	stored, err := q.GetProject(ctx, domain.ProjectID(id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read stored project %s: %w", id, err)
	}
	if !hasSealed(unmarshalProjectConfig(stored.Config).Env) {
		return nil
	}
	if s.envCodec == nil {
		return fmt.Errorf("write project %s: %w", id, errNoCodec)
	}
	if _, err := s.envCodec.cipherFor(ctx); err != nil {
		return fmt.Errorf("write project %s: %w", id, err)
	}
	return nil
}

var errNoCodec = fmt.Errorf("%w: no key store configured", ports.ErrProjectEnvLocked)

// openProjectRow decodes a stored row and opens its env. A sealed value it
// cannot open fails with ports.ErrProjectEnvLocked.
func (s *Store) openProjectRow(ctx context.Context, p gen.Project) (domain.ProjectRecord, error) {
	r := projectRowFromGen(p)
	env, err := s.envCodec.Open(ctx, r.ID, r.Config.Env)
	if err != nil && !r.ArchivedAt.IsZero() {
		// An archived project never launches: reading it (e.g. to re-add it)
		// must not fail on values it cannot open; re-adding rewrites the row.
		env, err = nil, nil
	}
	if err != nil {
		return domain.ProjectRecord{}, fmt.Errorf("project %s: %w", r.ID, err)
	}
	r.Config.Env = env
	return r, nil
}

// GetProjectWithoutEnv returns project id with its env left out, never opening
// or returning stored values. Removal uses it for a project whose env is locked.
func (s *Store) GetProjectWithoutEnv(ctx context.Context, id string) (domain.ProjectRecord, bool, error) {
	p, err := s.qr.GetProject(ctx, domain.ProjectID(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ProjectRecord{}, false, nil
	}
	if err != nil {
		return domain.ProjectRecord{}, false, fmt.Errorf("get project %s: %w", id, err)
	}
	r := projectRowFromGen(p)
	r.Config.Env = nil
	return r, true, nil
}

// EncryptProjectEnvAtRest seals every project's plaintext env values in place,
// active and archived, while sealing is on (first start under the hardened
// profile). Each project is its own transaction, so one that fails (key
// unavailable, damaged value) is left exactly as stored and the rest still
// migrate; rows already fully sealed are skipped, so it is idempotent. It
// returns how many projects it sealed and the first failure.
func (s *Store) EncryptProjectEnvAtRest(ctx context.Context) (int, error) {
	if !s.envCodec.sealing() {
		return 0, nil
	}
	ids, err := s.allProjectIDs(ctx)
	if err != nil {
		return 0, err
	}
	sealed := 0
	var firstErr error
	for _, id := range ids {
		did, err := s.sealProjectEnv(ctx, id)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if did {
			sealed++
		}
	}
	return sealed, firstErr
}

func (s *Store) allProjectIDs(ctx context.Context) ([]string, error) {
	rows, err := s.readDB.QueryContext(ctx, `SELECT id FROM projects ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list project ids: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("list project ids: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) sealProjectEnv(ctx context.Context, id string) (bool, error) {
	ctx = s.primeEnvKey(ctx, id)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	did := false
	err := s.inTxDB(ctx, "encrypt project env "+id, func(q *gen.Queries, db gen.DBTX) error {
		stored, err := q.GetProject(ctx, domain.ProjectID(id))
		if err != nil {
			return err
		}
		cfg := unmarshalProjectConfig(stored.Config)
		if allSealed(cfg.Env) {
			return nil
		}
		plain, err := s.envCodec.Open(ctx, id, cfg.Env)
		if err != nil {
			return err
		}
		if cfg.Env, err = s.envCodec.Seal(ctx, id, plain); err != nil {
			return err
		}
		encoded, err := marshalProjectConfig(cfg)
		if err != nil {
			return err
		}
		// Archived rows too, which UpdateProjectSettings skips.
		if _, err := db.ExecContext(ctx, `UPDATE projects SET config = ? WHERE id = ?`, encoded, id); err != nil {
			return err
		}
		did = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("encrypt env of project %s: %w", id, err)
	}
	return did, nil
}

// ClearProjectEnv drops every sealed env value of project id without the key,
// keeping its plaintext values and the rest of its config. It is the recovery
// for a project whose key is gone (Keychain item deleted or reset): removing
// the project clears it so it can be archived, re-added and its values
// re-entered. It reports whether anything was cleared.
func (s *Store) ClearProjectEnv(ctx context.Context, id string) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	cleared := false
	err := s.inTxDB(ctx, "clear project env "+id, func(q *gen.Queries, db gen.DBTX) error {
		stored, err := q.GetProject(ctx, domain.ProjectID(id))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		cfg := unmarshalProjectConfig(stored.Config)
		if !hasSealed(cfg.Env) {
			return nil
		}
		for name, value := range cfg.Env {
			if strings.HasPrefix(value, envCipherPrefix) {
				delete(cfg.Env, name)
			}
		}
		encoded, err := marshalProjectConfig(cfg)
		if err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, `UPDATE projects SET config = ? WHERE id = ?`, encoded, id); err != nil {
			return err
		}
		cleared = true
		return nil
	})
	if cleared {
		s.envCodec.forgetFailure() // the stored state changed: retry the key now
	}
	return cleared, err
}
