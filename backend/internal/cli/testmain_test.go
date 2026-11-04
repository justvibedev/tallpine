//go:build !e2e

package cli

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }
