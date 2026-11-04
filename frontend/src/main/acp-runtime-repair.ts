import { existsSync } from "node:fs";
import path from "node:path";

// Minimal slice of Electron's UtilityProcess that the repair needs.
export interface RepairChild {
	once(event: "exit", listener: (code: number) => void): unknown;
	once(event: "spawn", listener: () => void): unknown;
}

export type ForkScript = (script: string, options: { cwd: string; env: NodeJS.ProcessEnv; stdio: "inherit" }) => RepairChild;

export function acpRuntimeAdapterPath(appPath: string): string {
	return path.join(
		appPath,
		"resources",
		"acp-runtime",
		"node_modules",
		"@agentclientprotocol",
		"claude-agent-acp",
		"dist",
		"index.js",
	);
}

// Re-runs scripts/build-acp-runtime.mjs through utilityProcess.fork. Forking is
// not gated by the RunAsNode fuse, unlike re-executing process.execPath with
// ELECTRON_RUN_AS_NODE=1, which the packaged binary rejects. Dev only: the
// caller skips packaged builds, whose runtime is signed.
export async function repairAcpRuntime(
	appPath: string,
	fork: ForkScript,
	env: NodeJS.ProcessEnv = process.env,
): Promise<void> {
	if (existsSync(acpRuntimeAdapterPath(appPath))) return;
	const script = path.join(appPath, "scripts", "build-acp-runtime.mjs");
	if (!existsSync(script)) return;
	console.warn(`[tallpine] ACP runtime missing under ${appPath}; rebuilding`);
	await new Promise<void>((resolve) => {
		try {
			// npm_execpath makes the script run `process.execPath <npm-cli.js>`; in a
			// utility process that is Electron, not node. Dropping it makes the script
			// fall back to `npm` on PATH.
			const { ELECTRON_RUN_AS_NODE: _runAsNode, npm_execpath: _npmExecPath, ...childEnv } = env;
			const child = fork(script, { cwd: appPath, env: childEnv, stdio: "inherit" });
			child.once("exit", (code) => {
				if (code !== 0) console.error(`[tallpine] ACP runtime rebuild exited with ${code}`);
				resolve();
			});
		} catch (err) {
			console.error("[tallpine] ACP runtime rebuild failed to start:", err);
			resolve();
		}
	});
}
