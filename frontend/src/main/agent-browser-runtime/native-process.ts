/* Split out of agent-browser-runtime.ts (B297, P53). */

import { spawn } from "node:child_process";
import { COMMAND_TIMEOUT_MS, MAX_OUTPUT_BYTES, NativeProcessResult, runtimeError } from "./common";

export async function runNativeProcess(
	binaryPath: string,
	args: string[],
	environment: NodeJS.ProcessEnv,
	signal?: AbortSignal,
	timeoutMs = COMMAND_TIMEOUT_MS,
): Promise<NativeProcessResult> {
	return new Promise((resolve, reject) => {
		const child = spawn(binaryPath, args, {
			env: environment,
			stdio: ["ignore", "pipe", "pipe"],
			windowsHide: true,
		});
		let stdout: Buffer<ArrayBufferLike> = Buffer.alloc(0);
		let stderr: Buffer<ArrayBufferLike> = Buffer.alloc(0);
		let settled = false;
		const finish = (error?: Error, exitCode = -1) => {
			if (settled) return;
			settled = true;
			clearTimeout(timer);
			signal?.removeEventListener("abort", abort);
			if (error) reject(error);
			else
				resolve({
					stdout: stdout.toString("utf8"),
					stderr: stderr.toString("utf8"),
					exitCode,
				});
		};
		const append = (
			current: Buffer<ArrayBufferLike>,
			chunk: Buffer<ArrayBufferLike>,
		): Buffer<ArrayBufferLike> => {
			if (current.length + chunk.length > MAX_OUTPUT_BYTES) {
				child.kill();
				finish(runtimeError("AGENT_BROWSER_OUTPUT_TOO_LARGE", "agent-browser output exceeded Tallpine's limit"));
				return current;
			}
			return Buffer.concat([current, chunk]);
		};
		child.stdout.on("data", (chunk: Buffer) => {
			stdout = append(stdout, chunk);
		});
		child.stderr.on("data", (chunk: Buffer) => {
			stderr = append(stderr, chunk);
		});
		child.once("error", (error) => finish(error));
		child.once("close", (code) => finish(undefined, code ?? -1));
		// The native CLI starts a long-lived daemon. On Windows that daemon can
		// briefly retain inherited pipe handles after the short-lived CLI exits,
		// delaying Node's `close` event even though the command is complete.
		// `exit` is therefore the primary completion signal; one event-loop turn
		// still lets already-buffered stdout/stderr data handlers drain.
		child.once("exit", (code) => setImmediate(() => finish(undefined, code ?? -1)));
		const abort = () => {
			child.kill();
			finish(runtimeError("AGENT_BROWSER_CANCELLED", "agent-browser command was cancelled"));
		};
		signal?.addEventListener("abort", abort, { once: true });
		const timer = setTimeout(() => {
			child.kill();
			finish(runtimeError("AGENT_BROWSER_TIMEOUT", "agent-browser command timed out"));
		}, timeoutMs);
		if (signal?.aborted) abort();
	});
}
