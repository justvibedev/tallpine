package config

import "testing"

func TestLoadPhaseThreeToggles(t *testing.T) {
	t.Setenv("TALLPINE_TUI_VERSION_REFRESH", "")
	t.Setenv("TALLPINE_WINDOWS_UPDATE_WHEN_IDLE", "")
	t.Setenv("TALLPINE_UPDATE_WHEN_IDLE", "")
	cfg, err := Load()
	if err != nil || !cfg.TUIVersionRefresh || !cfg.UpdateWhenIdle {
		t.Fatalf("defaults: tui=%t idle=%t err=%v", cfg.TUIVersionRefresh, cfg.UpdateWhenIdle, err)
	}
	t.Setenv("TALLPINE_TUI_VERSION_REFRESH", "off")
	t.Setenv("TALLPINE_UPDATE_WHEN_IDLE", "off")
	if cfg, err = Load(); err != nil || cfg.TUIVersionRefresh || cfg.UpdateWhenIdle {
		t.Fatalf("off: tui=%t idle=%t err=%v", cfg.TUIVersionRefresh, cfg.UpdateWhenIdle, err)
	}
	// The older Windows-only name still turns it off.
	t.Setenv("TALLPINE_UPDATE_WHEN_IDLE", "")
	t.Setenv("TALLPINE_WINDOWS_UPDATE_WHEN_IDLE", "off")
	if cfg, err = Load(); err != nil || cfg.UpdateWhenIdle {
		t.Fatalf("legacy off: idle=%t err=%v", cfg.UpdateWhenIdle, err)
	}
}
