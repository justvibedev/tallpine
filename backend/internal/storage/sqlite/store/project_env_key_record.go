package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// The key record proves the key in the Keychain is the one this daemon
// created: a fingerprint (HMAC of a fixed label under the key) kept in
// Tallpine state, which the hardened profile's state fence keeps agents out
// of. A Keychain item planted by another same-user process (for example with
// `security add-generic-password -T <daemon>`) has no matching record.
const keyFingerprintLabel = "tallpine project-env key fingerprint v1"

func keyFingerprint(key []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(keyFingerprintLabel))
	return hex.EncodeToString(mac.Sum(nil))
}

// WithKeyRecord makes the codec accept a key only when its fingerprint matches
// the record at path. With no record and no sealed env stored yet, the key
// item is recreated (ports.SecretKeyRecreator) and recorded; with no record but
// sealed env, the key is refused.
func (c *EnvCodec) WithKeyRecord(path string) *EnvCodec {
	c.recordPath = path
	return c
}

// verifyKey checks key against the record; see WithKeyRecord. A recorded
// fingerprint that matches is accepted. Otherwise (no record, or a key that
// does not match it, e.g. the item was deleted or the Keychain reset) the key
// is replaced by a fresh one and recorded, but only while no sealed env is
// stored: with sealed env, a different key could not open it, so it is
// refused (remove the project to recover; see Store.ClearProjectEnv).
func (c *EnvCodec) verifyKey(ctx context.Context, key []byte) ([]byte, error) {
	if c.recordPath == "" {
		return key, nil
	}
	data, err := os.ReadFile(c.recordPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: read key record: %w", ports.ErrProjectEnvLocked, err)
	}
	if err == nil && subtle.ConstantTimeCompare([]byte(strings.TrimSpace(string(data))), []byte(keyFingerprint(key))) == 1 {
		return key, nil
	}
	if c.anySealed == nil {
		return nil, fmt.Errorf("%w: key not recorded", ports.ErrProjectEnvLocked)
	}
	sealed, err := c.anySealed(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ports.ErrProjectEnvLocked, err)
	}
	if sealed {
		return nil, fmt.Errorf("%w: the Keychain key is not the one that encrypted this env", ports.ErrProjectEnvLocked)
	}
	// Nothing sealed yet: never adopt an item that may have been planted.
	if r, ok := c.keys.(ports.SecretKeyRecreator); ok {
		if key, err = r.Recreate(ctx); err != nil {
			return nil, fmt.Errorf("%w: %w", ports.ErrProjectEnvLocked, err)
		}
	}
	if err := writeKeyRecord(c.recordPath, keyFingerprint(key)); err != nil {
		return nil, fmt.Errorf("%w: %w", ports.ErrProjectEnvLocked, err)
	}
	return key, nil
}

func writeKeyRecord(path, fingerprint string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("write key record: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(fingerprint+"\n"), 0o600); err != nil {
		return fmt.Errorf("write key record: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write key record: %w", err)
	}
	return nil
}

// anySealedEnv reports whether any active project holds a sealed env value.
// Archived projects do not count: they never launch, and their values cannot
// block a new key after the old one is lost (openProjectRow drops what an
// archived row cannot open, and re-adding it rewrites the row). It reads through the reader pool, so it is safe inside a
// write transaction.
func (s *Store) anySealedEnv(ctx context.Context) (bool, error) {
	rows, err := s.readDB.QueryContext(ctx, `SELECT config FROM projects WHERE archived_at IS NULL AND config LIKE ?`, "%"+envCipherPrefix+"%")
	if err != nil {
		return false, fmt.Errorf("scan for sealed env: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cfg sql.NullString
		if err := rows.Scan(&cfg); err != nil {
			return false, fmt.Errorf("scan for sealed env: %w", err)
		}
		if hasSealed(unmarshalProjectConfig(cfg).Env) {
			return true, nil
		}
	}
	return false, rows.Err()
}
