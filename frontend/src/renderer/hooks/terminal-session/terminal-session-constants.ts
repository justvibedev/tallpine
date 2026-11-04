import { getApiBaseUrl } from "../../lib/api-client";
import { createTerminalMux, muxUrlFromApiBase, type TerminalMux } from "../../lib/terminal-mux";

export const RETRY_BASE_MS = 500;
export const RETRY_MAX_MS = 8_000;
// The daemon runs a liveness probe and spawns the runtime client between
// mux.open() and the pane opening, so a stalled spawn must recover.
export const OPEN_TIMEOUT_MS = 3_000;
// Initial-replay gate. On attach the runtime replays the pane's state, and the
// daemon pumps it in 32KB reads (attachment.go copyOut) — so the renderer gets
// N WebSocket frames, N `write()` calls, and N separate event-loop turns. xterm
// parses each write atomically but the browser paints BETWEEN turns, so every
// frame boundary is a painted, further-scrolled state: the terminal visibly
// walks from mid-session down to the tail. Measured on a 1000-line replay: 25
// frames at 16ms spacing paint 25 distinct scroll positions; the same bytes as
// ONE write paint exactly 1, for ~2ms of parse.
//
// The first burst is buffered, joined in wire order, then parsed in bounded
// batches behind one cover. QUIET_MS or CAP_MS ends that coalesced phase; xterm
// then consumes late tail frames in order behind the same cover. The tail
// quiet/cap below decides when to reveal at the bottom.
export const REPLAY_QUIET_MS = 60;
export const REPLAY_CAP_MS = 750;
// After the coalesced replay batches land, keep the cover up while late replay
// frames stream into xterm. This removes the visible first-open walk without
// retaining another large duplicate replay buffer. A second cap bounds the
// cover for an agent that is already producing live output continuously.
export const REPLAY_TAIL_QUIET_MS = 180;
export const REPLAY_TAIL_CAP_MS = 750;
// Byte ceiling on the buffered burst. Attaching to a pane that is actively
// streaming (an agent mid-run) means every frame restarts the quiet window, so
// the time bounds alone would hold the entire burst. The byte cap limits the
// duplicate JS buffer; the write-batch cap below separately limits each xterm
// parser task. Real replays are far below this; only a pathological stream trips
// it, and tripping it ends only the coalesced phase: later bytes still render
// behind the cover until the tail settles.
export const REPLAY_MAX_BYTES = 1024 * 1024;
// A replay below the byte ceiling can still be large enough to monopolize the
// renderer in one xterm parser task. Feed it in bounded writes behind the same
// cover and yield between them; the user still sees one final reveal while
// fullscreen/window input remains responsive.
export const REPLAY_WRITE_BATCH_BYTES = 256 * 1024;
// Cover-only grace on the first replay byte. A pane that has produced NOTHING
// has no walk to hide, so holding the cover to the cap just shows a blank
// overlay — and past the pane's label delay (REPLAY_COVER_LABEL_MS in
// TerminalPane.tsx) a "Loading latest output…" label with nothing to load.
// Uncovering on this timer keeps that window short.
//
// It lifts the cover WITHOUT ending the gate: flushing here instead would clear
// `replayBuffering`, and a replay that then arrives late would go to xterm frame
// by frame — the exact walk this gate removes, with no cover left over it. So
// the burst stays coalesced either way and a late one lands as a single paint.
// Longer than the quiet window on purpose: `opened` fires from setPTY before
// copyOut, so the first byte is imminent but not instant, and a value near
// QUIET_MS would uncover panes that were about to draw.
export const REPLAY_FIRST_BYTE_MS = 250;

export function defaultCreateMux(): TerminalMux {
	// Resolved per connect, not per hook: a daemon restart can change the port.
	return createTerminalMux(muxUrlFromApiBase(getApiBaseUrl()));
}
