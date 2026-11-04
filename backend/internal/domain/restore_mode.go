package domain

// RestoreModeView is the API-facing restore-mode enum.
type RestoreModeView string

const (
	// RestoreModeViewNative restores a session using the runtime's native resume behavior.
	RestoreModeViewNative RestoreModeView = "native"
	// RestoreModeViewSavedPrompt restores a session by replaying the saved prompt.
	RestoreModeViewSavedPrompt RestoreModeView = "saved_prompt"
	// RestoreModeViewFresh restores a session by starting from a fresh runtime state.
	RestoreModeViewFresh RestoreModeView = "fresh"
)
