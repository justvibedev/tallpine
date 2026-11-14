//go:build !darwin && !linux

package peeradmit

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }
