//go:build !darwin || !cgo

package secretkey

import (
	"context"
	"fmt"
	"runtime"

	"tallpine.eu/tallpine/backend/internal/ports"
)

type unavailable struct{}

// New returns the platform key store: here none, so encrypted env fails closed.
func New(Options) ports.SecretKeyStore { return unavailable{} }

func (unavailable) Key(context.Context) ([]byte, error) {
	return nil, fmt.Errorf("%w: no keychain on %s (or built without cgo)", ports.ErrSecretKeyUnavailable, runtime.GOOS)
}
