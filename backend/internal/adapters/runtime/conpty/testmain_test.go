//go:build !darwin && !linux

package conpty

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }
