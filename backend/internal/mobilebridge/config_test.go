package mobilebridge

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
	"time"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := Path(dir)
	want := State{Enabled: true, LegacyPasswordHash: HashPassword("abc"), LegacyUntil: time.Unix(1_800_000_000, 0).UTC(), LastPort: 3011}
	key, err := LoadLegacyKey(p)
	if err != nil {
		t.Fatal(err)
	}
	want.LegacyPasswordHash = SealLegacyHash(key, HashPassword("abc"))
	if err := Save(p, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != want {
		t.Fatalf("round trip: got %+v want %+v", got, want)
	}
	info, _ := os.Stat(p)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v want 0600", info.Mode().Perm())
	}
}

func TestLoadMissingIsZero(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "mobile", "config.json"))
	if err != nil || got != (State{RequireDevicePairing: true}) {
		t.Fatalf("missing file: got %+v err %v", got, err)
	}
}

func TestGeneratePasswordFormat(t *testing.T) {
	pw, err := GeneratePassword()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9]{8}$`).MatchString(pw) {
		t.Fatalf("password %q not 8 alnum", pw)
	}
}

func TestPasswordMatches(t *testing.T) {
	pw, _ := GeneratePassword()
	h := HashPassword(pw)
	if !PasswordMatches(h, pw) {
		t.Fatal("expected match")
	}
	if PasswordMatches(h, pw+"x") {
		t.Fatal("expected mismatch")
	}
}

func TestGeneratePasswordUsesEveryCharacterUniformly(t *testing.T) {
	counts := map[rune]int{}
	const rounds = 4000
	for i := 0; i < rounds; i++ {
		pw, err := GeneratePassword()
		if err != nil {
			t.Fatal(err)
		}
		if len(pw) != 8 {
			t.Fatalf("password %q has length %d", pw, len(pw))
		}
		for _, r := range pw {
			counts[r]++
		}
	}
	if len(counts) != len(pwAlphabet) {
		t.Fatalf("saw %d distinct characters, want %d", len(counts), len(pwAlphabet))
	}
	// 32000 draws over 62 characters: about 516 each. The old modulo mapping
	// gave the first 8 characters 5/4 the weight of the rest; a uniform draw
	// stays well inside 20% of the mean.
	mean := float64(rounds*8) / float64(len(pwAlphabet))
	for r, n := range counts {
		if float64(n) < 0.8*mean || float64(n) > 1.2*mean {
			t.Errorf("character %q drawn %d times, mean %.0f", r, n, mean)
		}
	}
}
