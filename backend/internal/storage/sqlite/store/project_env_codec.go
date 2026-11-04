package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// envCipherPrefix marks a project env value sealed by EnvCodec. A value without
// it is plaintext (written with the profile off, or before migration), so rows
// may mix both.
const envCipherPrefix = "tpenc:v1:"

// EnvCodec encrypts ProjectConfig.Env values with AES-256-GCM at the store
// boundary. Each value is sealed with its own nonce; the additional data binds
// it to its project id and variable name, so a value copied to another project
// or key fails to open. Values are sealed only while encrypt reports true (the
// managed hardened profile); sealed values are opened whenever present, so
// removing the profile leaves them encrypted but readable.
type EnvCodec struct {
	keys    ports.SecretKeyStore
	encrypt func() bool

	// mu guards aead and the failure cache for short reads and writes only;
	// loadMu serializes the (possibly slow) key read.
	mu     sync.Mutex
	loadMu sync.Mutex
	aead   cipher.AEAD
	// failedAt/failure cache a failed key read for keyRetryAfter, so a denied
	// or locked Keychain fails fast instead of prompting on every read.
	failedAt time.Time
	failure  error
	now      func() time.Time
	// recordPath and anySealed back the key record (WithKeyRecord).
	recordPath string
	anySealed  func(context.Context) (bool, error)
}

// keyRetryAfter is how long a failed key read is reused before retrying.
const keyRetryAfter = time.Minute

// NewEnvCodec returns a codec over keys that seals new writes while encrypt
// reports true.
func NewEnvCodec(keys ports.SecretKeyStore, encrypt func() bool) *EnvCodec {
	return &EnvCodec{keys: keys, encrypt: encrypt, now: time.Now}
}

func (c *EnvCodec) sealing() bool { return c != nil && c.encrypt != nil && c.encrypt() }

// cipherFor returns the AEAD, reading the key once. A failed read is reused
// for keyRetryAfter, then retried (the Keychain may be unlocked later).
func (c *EnvCodec) cipherFor(ctx context.Context) (cipher.AEAD, error) {
	if aead, ok, err := c.cached(); ok {
		return aead, err
	}
	if cachedOnly(ctx) {
		// Under the store write lock: never wait for a key read in progress.
		return nil, fmt.Errorf("%w: key not loaded", ports.ErrProjectEnvLocked)
	}
	c.loadMu.Lock() // one Keychain read at a time; state stays readable meanwhile
	defer c.loadMu.Unlock()
	if aead, ok, err := c.cached(); ok {
		return aead, err
	}
	aead, err := c.loadCipher(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		// A cancelled or timed-out caller says nothing about the Keychain.
		if ctx.Err() == nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			c.failure, c.failedAt = err, c.now()
		}
		return nil, err
	}
	c.aead, c.failure = aead, nil
	return aead, nil
}

// cached answers from the loaded key or a recent failure; ok is false when the
// key has to be read. It holds mu only briefly, never across a key read.
func (c *EnvCodec) cached() (cipher.AEAD, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.aead != nil {
		return c.aead, true, nil
	}
	if c.failure != nil && c.now().Sub(c.failedAt) < keyRetryAfter {
		return nil, true, c.failure
	}
	return nil, false, nil
}

func (c *EnvCodec) loadCipher(ctx context.Context) (cipher.AEAD, error) {
	key, err := c.keys.Key(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ports.ErrProjectEnvLocked, err)
	}
	if key, err = c.verifyKey(ctx, key); err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: key is %d bytes, want 32", ports.ErrProjectEnvLocked, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ports.ErrProjectEnvLocked, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ports.ErrProjectEnvLocked, err)
	}
	return aead, nil
}

// forgetFailure drops a cached key failure so the next read retries at once.
func (c *EnvCodec) forgetFailure() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.failure = nil
	c.mu.Unlock()
}

func envAAD(projectID, name string) []byte { return []byte(projectID + "\x00" + name) }

// hasSealed reports whether any value in env is sealed.
func hasSealed(env map[string]string) bool {
	for _, v := range env {
		if strings.HasPrefix(v, envCipherPrefix) {
			return true
		}
	}
	return false
}

// allSealed reports whether every value in env is sealed (vacuously true).
func allSealed(env map[string]string) bool {
	for _, v := range env {
		if !strings.HasPrefix(v, envCipherPrefix) {
			return false
		}
	}
	return true
}

// Seal returns env as it is stored: every value sealed while sealing is on,
// otherwise env unchanged (plaintext).
func (c *EnvCodec) Seal(ctx context.Context, projectID string, env map[string]string) (map[string]string, error) {
	if len(env) == 0 || !c.sealing() {
		return env, nil
	}
	aead, err := c.cipherFor(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(env))
	for name, value := range env {
		nonce := make([]byte, aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return nil, fmt.Errorf("seal project env: %w", err)
		}
		sealed := aead.Seal(nonce, nonce, []byte(value), envAAD(projectID, name))
		out[name] = envCipherPrefix + base64.RawStdEncoding.EncodeToString(sealed)
	}
	return out, nil
}

// Open returns env with every sealed value decrypted; plaintext values pass
// through. It fails with ErrProjectEnvLocked when a value is sealed and the key
// is unavailable, and with an error when a sealed value does not open (wrong
// project, wrong name, tampered).
func (c *EnvCodec) Open(ctx context.Context, projectID string, env map[string]string) (map[string]string, error) {
	if !hasSealed(env) {
		// Legacy plaintext under the hardened profile is refused until it can
		// be sealed, so it never reaches settings or launch input unprotected.
		if c.sealing() && len(env) > 0 {
			if _, err := c.cipherFor(ctx); err != nil {
				return nil, err
			}
		}
		return env, nil
	}
	if c == nil {
		return nil, fmt.Errorf("%w: no key store configured", ports.ErrProjectEnvLocked)
	}
	aead, err := c.cipherFor(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(env))
	for name, value := range env {
		encoded, ok := strings.CutPrefix(value, envCipherPrefix)
		if !ok {
			out[name] = value
			continue
		}
		plain, err := openValue(aead, encoded, envAAD(projectID, name))
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ports.ErrProjectEnvLocked, name, err)
		}
		out[name] = plain
	}
	return out, nil
}

var errEnvCiphertext = errors.New("sealed value does not open")

func openValue(aead cipher.AEAD, encoded string, aad []byte) (string, error) {
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(raw) < aead.NonceSize() {
		return "", errEnvCiphertext
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], aad)
	if err != nil {
		return "", errEnvCiphertext
	}
	return string(plain), nil
}

type cachedOnlyKey struct{}

// cachedKeyOnly marks ctx so the codec uses only an already loaded key.
func cachedKeyOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, cachedOnlyKey{}, true)
}

func cachedOnly(ctx context.Context) bool { v, _ := ctx.Value(cachedOnlyKey{}).(bool); return v }
