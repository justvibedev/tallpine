// IPC contract for the shell's daemon WebSocket bridge (B176); see
// main/daemon-ws-bridge.ts and renderer/lib/daemon-websocket.ts.

export const DAEMON_WS_OPEN_CHANNEL = "daemon-ws:open";
export const DAEMON_WS_SEND_CHANNEL = "daemon-ws:send";
export const DAEMON_WS_CLOSE_CHANNEL = "daemon-ws:close";
export const DAEMON_WS_EVENT_CHANNEL = "daemon-ws:event";

export type DaemonWsEvent =
	| { id: number; type: "open" }
	| { id: number; type: "message"; data: string }
	| { id: number; type: "error" }
	| { id: number; type: "close"; code: number; reason: string };
