package daemon

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/secretkey"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// keyRecordFile, in the data dir, holds the fingerprint of the key the daemon
// created, so a Keychain item planted by another process is refused.
const keyRecordFile = "project-env-key.fingerprint"

// wireProjectEnvEncryption seals project env values at rest while the managed
// hardened profile is in force, with the key in a daemon-owned Keychain item.
// Already-stored plaintext is encrypted in place in the background, so a
// Keychain prompt never blocks startup; a project whose env cannot be sealed
// or opened fails closed (PROJECT_ENV_LOCKED) until the key is available.
func wireProjectEnvEncryption(ctx context.Context, st *store.Store, engine *agentpolicy.Engine, dataDir string, log *slog.Logger) {
	hardened := func() bool { return engine.ManagedProfile() == agentpolicy.ProfileHardened }
	codec := store.NewEnvCodec(secretkey.New(secretkey.Options{}), hardened).
		WithKeyRecord(filepath.Join(dataDir, keyRecordFile))
	st.SetEnvCodec(codec)
	go migrateProjectEnvLoop(ctx, hardened, st.EncryptProjectEnvAtRest, projectEnvMigrateInterval, log)
}

// projectEnvMigrateInterval is how often the migration retries or re-checks
// whether the hardened profile has become active.
const projectEnvMigrateInterval = 30 * time.Second

// migrateProjectEnvLoop seals stored plaintext env whenever the hardened
// profile is active. After a pass that succeeds it idles until the profile
// goes off and on again; after a failure (locked Keychain) it retries every
// interval, logging only when the failure first appears or changes. It
// notices a profile turned on after daemon start.
func migrateProjectEnvLoop(ctx context.Context, hardened func() bool, migrate func(context.Context) (int, error), every time.Duration, log *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	done, lastErr := false, ""
	for {
		switch {
		case !hardened():
			done, lastErr = false, ""
		case !done:
			n, err := migrate(ctx)
			switch {
			case err != nil:
				if msg := err.Error(); msg != lastErr {
					log.Error("hardened profile: encrypt project env at rest; retrying; affected projects fail closed (PROJECT_ENV_LOCKED)", "projects_sealed", n, "err", err)
					lastErr = msg
				}
			default:
				done, lastErr = true, ""
				if n > 0 {
					log.Info("hardened profile: encrypted project env at rest", "projects_sealed", n)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
