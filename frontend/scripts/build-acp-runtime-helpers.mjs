import { mkdtempSync, readFileSync, rmSync, unlinkSync, writeFileSync } from "node:fs";
import { join, win32 } from "node:path";

const ROOT_BUILD_TOOLS = ["corepack", "corepack.cmd", "npm", "npm.cmd", "npx", "npx.cmd"];
const BIN_BUILD_TOOLS = ["corepack", "npm", "npx"];
const BUILD_ONLY_CONTENT = ["include", "lib", "node_modules", "share", "CHANGELOG.md", "README.md"];

export function runtimeSourceFiles() {
	return ["package.json", "package-lock.json"];
}

export function createWorkDirectory(outputRoot) {
	// Windows runners commonly keep the checkout on D: and the OS temp directory
	// on C:. Keep extraction beside its destination so the final rename remains
	// an atomic, same-filesystem operation on every platform.
	return mkdtempSync(join(outputRoot, ".node-download-"));
}

export function npmInvocation(
	args,
	{
		platform = process.platform,
		execPath = process.execPath,
		npmExecPath = process.env.npm_execpath,
		commandInterpreter = process.env.ComSpec,
	} = {},
) {
	// npm exposes the JavaScript entry point of the npm instance running this
	// package script. Invoking it with Node avoids the npm.cmd shell boundary on
	// Windows and keeps the nested install on the same npm version as the build.
	if (npmExecPath) {
		return { command: execPath, args: [npmExecPath, ...args] };
	}
	if (platform === "win32") {
		return {
			command: commandInterpreter || "cmd.exe",
			args: ["/d", "/s", "/c", "npm.cmd", ...args],
		};
	}
	return { command: "npm", args };
}

/**
 * Preserve Claude's API-retry backoff in the ACP session-failure extension.
 *
 * claude-agent-acp 0.99 publishes retry count and category, but drops the
 * SDK's retry_delay_ms before the event reaches ACP clients. AO patches the
 * pinned compiled adapter during packaging so the extension's ordinary
 * `details` field carries the missing timing. The narrow match is a
 * deliberate upgrade tripwire: if upstream changes this code, packaging fails
 * instead of silently returning to an unobservable retry loop.
 */
export function patchClaudeRetryDetails(adapterPath) {
	const source = readFileSync(adapterPath, "utf8");
	const caseStart = source.indexOf('case "api_retry": {');
	const caseEnd = source.indexOf('case "model_refusal_fallback":', caseStart);
	if (caseStart < 0 || caseEnd < 0) {
		throw new Error("claude-agent-acp no longer contains the expected api_retry block");
	}

	let block = source.slice(caseStart, caseEnd);
	if (block.includes("const retryDetails =")) return false;

	const publish = 'await publishSessionFailure(kind, { title, severity: "warning" });';
	if (block.split(publish).length !== 2 || !block.includes("message.error_status === null")) {
		throw new Error("claude-agent-acp api_retry block no longer matches AO's retry patch");
	}

	const retryDetails = [
		"const retryDelay = message.retry_delay_ms >= 1000",
		"                                    ? `${Number((message.retry_delay_ms / 1000).toFixed(1))}s`",
		"                                    : `${message.retry_delay_ms}ms`;",
		'                                const retryDetails = `${message.error_status === null ? "Connection error." : `API error ${message.error_status}.`} Trying again in ${retryDelay}.`;',
		'                                await publishSessionFailure(kind, { title, severity: "warning", details: retryDetails });',
	].join("\n");
	block = block.replace(publish, retryDetails);

	writeFileSync(adapterPath, source.slice(0, caseStart) + block + source.slice(caseEnd));
	return true;
}

/**
 * claude-agent-acp 0.99 reports the last assistant message's token usage as
 * context occupancy. The SDK's getContextUsage returns its current retained
 * context estimate. Publish that snapshot after each result so AO's existing
 * usage_update projection receives the provider's own estimate. The summary
 * detail is deliberate: the default ("full") counts every category with the
 * token-count API, which would delay each turn's end by up to the timeout. The
 * full breakdown belongs to an on-demand request, not this per-result hook.
 */
export function patchClaudeContextUsage(adapterPath) {
	const source = readFileSync(adapterPath, "utf8");
	const start = source.indexOf("// Send usage_update notification");
	const end = source.indexOf("if (session.cancelled) {", start);
	if (start < 0 || end < 0 || source.indexOf("// Send usage_update notification", start + 1) >= 0) {
		throw new Error("claude-agent-acp result usage block no longer matches AO's context patch");
	}
	const block = source.slice(start, end);
	if (source.includes("// AO: use the SDK's context snapshot.")) return false;
	if (!block.includes("used: lastAssistantTotalUsage,") || !block.includes("size: session.contextWindowSize,")) {
		throw new Error("claude-agent-acp result usage block no longer matches AO's context patch");
	}

	const snapshot = [
		"// AO: use the SDK's context snapshot.",
		"                            let contextUsageTimer;",
		"                            try {",
		"                                const contextUsage = await Promise.race([",
		"                                    session.query.getContextUsage({ detail: 'summary' }),",
		"                                    new Promise((_, reject) => {",
		"                                        contextUsageTimer = setTimeout(() => reject(new Error('Claude SDK context usage timed out')), 2000);",
		"                                    }),",
		"                                ]);",
		"                                if (Number.isFinite(contextUsage.totalTokens) && contextUsage.totalTokens > 0 &&",
		"                                    Number.isFinite(contextUsage.rawMaxTokens) && contextUsage.rawMaxTokens > 0) {",
		"                                    lastAssistantTotalUsage = contextUsage.totalTokens;",
		"                                    session.contextWindowSize = contextUsage.rawMaxTokens;",
		"                                    session.contextWindowAuthoritative = true;",
		"                                }",
		"                            } catch (error) {",
		"                                this.logger.error('Failed to fetch Claude SDK context usage:', error);",
		"                            } finally {",
		"                                clearTimeout(contextUsageTimer);",
		"                            }",
		"                            ",
	].join("\n");
	writeFileSync(adapterPath, source.slice(0, start) + snapshot + source.slice(start));
	return true;
}

// Reuse the pinned bridge's native task registry. Ending its process loses the
// task monitor even when a background shell's process group survives. Claude
// Code also starts cycles on its own after an AO turn, for example to answer a
// finished task's notification. AO has no turn for those, and the finished
// task has already left the registry, so the SDK's last reported state is the
// only signal that the agent is still working. A query that never reports idle
// again keeps its host awake, which is the safe side.
export function patchClaudeHibernationCheck(adapterPath) {
	let source = readFileSync(adapterPath, "utf8");
	const method = '"_ao/session/can_hibernate"';
	if (source.includes(method)) return false;
	const marker = ".onRequest(GOAL_CONTROL_METHOD, { parse: parseGoalRequest }, (ctx) => agent.goal(ctx.params))";
	// A finished task leaves the registry through settleLiveBackgroundTask and the
	// reconcile sweep; both delete it by id.
	const ended = "session.liveBackgroundTasks.delete(taskId);";
	if (!source.includes(marker) || !source.includes("session.liveBackgroundTasks.set(taskId") ||
		!source.includes("session.lastSessionState = message.state;") || source.split(ended).length !== 3) {
		throw new Error("claude-agent-acp no longer matches AO's native-task hibernation check");
	}
	source = source.replaceAll(ended, `${ended} session.aoTaskEndedAt = Date.now();`);
	const registration = `.onRequest(${method}, { parse: params => params }, (ctx) => {
            const id = ctx.params?.sessionId;
            const session = typeof id === "string" && Object.hasOwn(agent.sessions, id) ? agent.sessions[id] : undefined;
            if (!session || session.queryClosed || !(session.liveBackgroundTasks instanceof Map)) {
                throw RequestError.invalidParams(undefined, "Live Claude session unavailable.");
            }
            const sdkIdle = session.lastSessionState === undefined || session.lastSessionState === "idle";
            // The SDK reports the notification cycle as running 79 ms after the
            // task leaves the registry (measured on Claude Code 2.1.296).
            // ponytail: a fixed hold; a slower CLI needs a pending-cycle signal.
            const cyclePending = Date.now() - (session.aoTaskEndedAt ?? 0) < 10000;
            return { canHibernate: sdkIdle && !cyclePending && !Array.from(session.liveBackgroundTasks.values()).some(task => !task.endedPerLevel) };
        })
        `;
	writeFileSync(adapterPath, source.replace(marker, registration + marker));
	return true;
}

/**
 * Let AO change a running Claude chat's auto-compaction window.
 *
 * Claude Code's SDK can change the window live (Query.applyFlagSettings with
 * autoCompactWindow), but claude-agent-acp 0.99 offers no ACP method for the compaction window (it has config options for model, effort, fast mode and ultracode, but none for this, and adding one means threading it through every buildConfigOptions call and refresh), and
 * the CLI lets CLAUDE_CODE_AUTO_COMPACT_WINDOW take precedence over the setting
 * ("is set and takes precedence"). So the patch does two things:
 *   1. when a session starts with that variable in its environment (AO passes
 *      its choice there), it moves the value into the programmatic settings
 *      instead, so a later change can take effect;
 *   2. it adds the `_ao/session/set_auto_compact_window` request, which calls
 *      applyFlagSettings and then reads the window Claude Code itself reports
 *      (getContextUsage, summary). It answers applied:true only if that window
 *      moved: Claude Code 2.1.312 accepts the flag and keeps its launch window,
 *      measured live (launch 400000, flag 300000, reported max stayed 400000, also
 *      after a turn). applied:false also covers a variable that could not be
 *      moved (settings given as a file path). AO then falls back to "applies
 *      after restart", which does take effect.
 * To drop this patch once upstream ships an equivalent ACP method, delete this
 * function, its call in build-acp-runtime.mjs, and the Go caller's method name.
 */
export function patchClaudeAutoCompactWindow(adapterPath) {
	let source = readFileSync(adapterPath, "utf8");
	const method = '"_ao/session/set_auto_compact_window"';
	if (source.includes(method)) return false;
	const goal = ".onRequest(GOAL_CONTROL_METHOD, { parse: parseGoalRequest }, (ctx) => agent.goal(ctx.params))";
	const optionsStart = "        const options = {\n            systemPrompt,\n            settingSources:";
	const settingsDecl = "        const settings = resolvedProvider\n";
	if (!source.includes(goal) || source.split(optionsStart).length !== 2 || source.split(settingsDecl).length !== 2 ||
		!source.includes("const providerCacheKey = providerCacheKeyFor(env);")) {
		throw new Error("claude-agent-acp no longer matches AO's auto-compact window patch");
	}
	// 0.99 reads settings into a const; the fold below needs to add to it.
	source = source.replace(settingsDecl, "        let settings = resolvedProvider\n");
	const fold = `        // AO: the compaction window must live in settings, not the environment, to be changeable live.
        {
            const raw = env.CLAUDE_CODE_AUTO_COMPACT_WINDOW;
            if (raw !== undefined && raw !== "") {
                const tokens = Number(raw);
                const foldable = Number.isInteger(tokens) && tokens > 0 && (settings === undefined || typeof settings === "object");
                if (foldable) {
                    delete env.CLAUDE_CODE_AUTO_COMPACT_WINDOW;
                    settings = { ...settings, autoCompactWindow: tokens };
                }
                (this.aoCompactWindows ??= new Map()).set(sessionId, { tokens: foldable ? tokens : null, launch: foldable ? tokens : undefined, locked: !foldable });
            } else {
                (this.aoCompactWindows ??= new Map()).set(sessionId, { tokens: null, launch: null, locked: false });
            }
        }
`;
	source = source.replace(optionsStart, fold + optionsStart);
	const registration = `.onRequest(${method}, { parse: params => params }, async (ctx) => {
            const id = ctx.params?.sessionId;
            const session = typeof id === "string" && Object.hasOwn(agent.sessions, id) ? agent.sessions[id] : undefined;
            if (!session || session.queryClosed) {
                throw RequestError.invalidParams(undefined, "Live Claude session unavailable.");
            }
            const tokens = ctx.params.tokens;
            if (tokens !== null && !(Number.isInteger(tokens) && tokens >= 100000 && tokens <= 1000000)) {
                throw RequestError.invalidParams(undefined, "tokens must be null or an integer from 100000 to 1000000.");
            }
            const state = agent.aoCompactWindows?.get(id);
            if (state?.locked) return { applied: false, tokens: state.tokens };
            // Claude Code 2.1.312 accepts the flag setting and keeps compacting at its
            // launch window, so the SDK's own report decides whether the change took.
            const before = await session.query.getContextUsage({ detail: "summary" });
            await session.query.applyFlagSettings({ autoCompactWindow: tokens });
            const after = await session.query.getContextUsage({ detail: "summary" });
            const moved = after.maxTokens !== before.maxTokens;
            // A reset (null) takes if the window moved, or if the session launched
            // without a window: the CLI is then at its own default already, which is
            // the state a reattached Auto chat asks for. A launch window the CLI keeps
            // is not a reset. rawMaxTokens is no guide: it follows the configured
            // window (launch 400000 reported raw 400000), not the model's.
            const took = tokens === null ? moved || state?.launch === null : after.maxTokens === tokens;
            if (took && state) state.tokens = tokens;
            return { applied: took, tokens: state ? state.tokens : null };
        })
        `;
	writeFileSync(adapterPath, source.replace(goal, registration + goal));
	return true;
}

/**
 * Let AO read a running Claude chat's context breakdown on request.
 *
 * Claude Code computes exactly what /context shows (system prompt, tools, MCP,
 * skills, memory files, agents, messages split into tool calls, results and
 * attachments, free space and the compaction buffer), and the SDK returns it
 * from Query.getContextUsage, but claude-agent-acp 0.99 never forwards it to
 * the client. The patch adds `_ao/session/context_usage`, which asks for
 * detail "full" (each category counted with the token-count API, which is why
 * it is a request and not part of every turn: the per-result snapshot uses
 * "summary") and answers the SDK's reply without its display grid. A bridge
 * without the patch answers method-not-found and AO shows totals only.
 * To drop it once upstream ships an equivalent ACP method, delete this
 * function and its call in build-acp-runtime.mjs and rename the Go method name.
 */
export function patchClaudeContextBreakdown(adapterPath) {
	let source = readFileSync(adapterPath, "utf8");
	const method = '"_ao/session/context_usage"';
	if (source.includes(method)) return false;
	const goal = ".onRequest(GOAL_CONTROL_METHOD, { parse: parseGoalRequest }, (ctx) => agent.goal(ctx.params))";
	if (!source.includes(goal)) {
		throw new Error("claude-agent-acp no longer matches AO's context breakdown patch");
	}
	const registration = `.onRequest(${method}, { parse: params => params }, async (ctx) => {
            const id = ctx.params?.sessionId;
            const session = typeof id === "string" && Object.hasOwn(agent.sessions, id) ? agent.sessions[id] : undefined;
            if (!session || session.queryClosed || typeof session.query?.getContextUsage !== "function") {
                throw RequestError.invalidParams(undefined, "Live Claude session unavailable.");
            }
            const { gridRows: _display, ...usage } = await session.query.getContextUsage({ detail: "full" });
            return usage;
        })
        `;
	writeFileSync(adapterPath, source.replace(goal, registration + goal));
	return true;
}

/**
 * Do not fail a turn over Claude Code's own `confirm_*` prompts.
 *
 * Claude Code asks some questions itself, not through the model: for example
 * the refusal-fallback prompt ("Switch to <model> and keep going?") after a
 * safeguard refusal. It sends that question through canUseTool as an
 * AskUserQuestion whose id starts with `confirm_` (the CLI's synthetic-prompt
 * prefix) and never sends a tool_result for it. claude-agent-acp 0.99 records
 * every permission-routed call as a dispatched foreground tool, so at end_turn
 * it treats the prompt as stuck and fails a turn whose replies all arrived
 * ("Claude ended the turn without returning results for tool calls:
 * confirm_..."). The patch closes those prompts as completed and lets the turn
 * end normally. Real tool calls are still checked.
 */
export function patchClaudeSyntheticPromptTurnEnd(adapterPath) {
	const source = readFileSync(adapterPath, "utf8");
	const marker = "// Tallpine: Claude Code's own confirm_ prompts get no tool_result.";
	if (source.includes(marker)) return false;
	const original = [
		"const abandoned = unfinished.filter((id) => !session.dispatchedToolCalls?.has(id));",
		"                const stuck = unfinished.filter((id) => session.dispatchedToolCalls?.has(id));",
	].join("\n");
	if (source.split(original).length !== 2 || !source.includes("const closeToolCall = async (toolCallId, report) => {")) {
		throw new Error("claude-agent-acp end-of-turn tool check no longer matches Tallpine's synthetic prompt patch");
	}
	const patched = [
		marker,
		'                const synthetic = unfinished.filter((id) => id.startsWith("confirm_"));',
		"                for (const toolCallId of synthetic) {",
		'                    await closeToolCall(toolCallId, { sessionUpdate: "tool_call_update", toolCallId, status: "completed" });',
		"                    if (turn.settled || session.activeTurn !== turn)",
		"                        return;",
		"                    if (session.cancelled) {",
		'                        await settleActive({ ...result, stopReason: "cancelled" });',
		"                        return;",
		"                    }",
		"                }",
		'                const abandoned = unfinished.filter((id) => !id.startsWith("confirm_") && !session.dispatchedToolCalls?.has(id));',
		'                const stuck = unfinished.filter((id) => !id.startsWith("confirm_") && session.dispatchedToolCalls?.has(id));',
	].join("\n");
	writeFileSync(adapterPath, source.replace(original, patched));
	return true;
}

export function pruneNodeDistribution(nodeRoot) {
	// The Unix archives expose npm/corepack as bin/ symlinks into lib/. Remove
	// the entry points before their targets so packagers never see dangling
	// links. Windows keeps the launchers at the archive root instead.
	for (const name of BIN_BUILD_TOOLS) {
		removeFile(join(nodeRoot, "bin", name));
	}
	for (const name of ROOT_BUILD_TOOLS) {
		removeFile(join(nodeRoot, name));
	}
	for (const name of BUILD_ONLY_CONTENT) {
		rmSync(join(nodeRoot, name), { recursive: true, force: true });
	}
}

function removeFile(path) {
	try {
		// unlink removes a symlink itself even when its target is already absent.
		unlinkSync(path);
	} catch (error) {
		if (error?.code !== "ENOENT") throw error;
	}
}

export function archiveExtraction(
	archivePath,
	workDir,
	{ platform = process.platform, systemRoot = process.env.SystemRoot } = {},
) {
	// Windows ships bsdtar as System32\tar.exe and it reads zip. PowerShell's
	// Expand-Archive is the obvious alternative but is bound by MAX_PATH: with
	// LongPathsEnabled=0 and a deep checkout, Node's bundled npm tree exceeds
	// 260 characters and extraction fails without a non-zero exit, so the build
	// only discovers it later, as a missing directory. bsdtar handles the same
	// archive at the same depth.
	if (platform === "win32") {
		if (!systemRoot) throw new Error("SystemRoot is required for Windows archive extraction");
		// Git Bash can put GNU tar ahead of System32 on PATH.
		return { command: win32.join(systemRoot, "System32", "tar.exe"), args: ["-xf", archivePath, "-C", workDir] };
	}
	return { command: "tar", args: ["-xzf", archivePath, "-C", workDir] };
}
