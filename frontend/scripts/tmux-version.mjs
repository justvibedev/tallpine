// Single source of truth for the bundled tmux release.
//
// Two places need it and must never drift: build-tmux.mjs (what gets compiled)
// and forge.config.ts's postPackage gate (what the packaged app is verified
// against). A duplicated literal in the gate is what turns a deliberate version
// bump into a packaging failure.
//
// tmux's client/server protocol is not compatible across versions, so this pin
// also decides which running tmux servers a packaged Tallpine can still talk to.
export const BUNDLED_TMUX_VERSION = "3.8";
export const BUNDLED_TMUX_SHA256 = "e79c699c7e949dccd0a4a125e17b8d1261e311b16979dbc8eb34542f3966d82e";
