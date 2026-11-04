#!/usr/bin/env node
// Minimal Agent Client Protocol agent for the phone e2e. The daemon launches it
// through TALLPINE_CLAUDE_ACP_COMMAND in place of claude-agent-acp, so a chat
// session exists without any model, network or credentials. It answers every
// prompt with "echo: <prompt text>".
import { createInterface } from "node:readline";

const send = (msg) => process.stdout.write(`${JSON.stringify({ jsonrpc: "2.0", ...msg })}\n`);
const sessionId = "fake-acp-session";

const handlers = {
	initialize: () => ({ protocolVersion: 1, agentCapabilities: { sessionCapabilities: { resume: {} } } }),
	authenticate: () => ({}),
	"session/new": () => ({ sessionId }),
	"session/resume": () => ({}),
	"session/cancel": () => ({}),
	"session/set_mode": () => ({}),
	"session/set_model": () => ({}),
	"session/set_config_option": () => ({ configOptions: [] }),
	"session/prompt": (params) => {
		const text = (params.prompt ?? []).map((b) => b.text ?? "").join("");
		send({
			method: "session/update",
			params: { sessionId, update: { sessionUpdate: "agent_message_chunk", content: { type: "text", text: `echo: ${text}` } } },
		});
		return { stopReason: "end_turn" };
	},
};

createInterface({ input: process.stdin }).on("line", (line) => {
	if (!line.trim()) return;
	let msg;
	try { msg = JSON.parse(line); } catch { return; }
	if (msg.id === undefined || msg.method === undefined) return; // notifications and responses
	const handler = handlers[msg.method];
	if (!handler) return send({ id: msg.id, error: { code: -32601, message: `method not found: ${msg.method}` } });
	send({ id: msg.id, result: handler(msg.params ?? {}) });
});
