import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterAll } from "vitest";

// Agents run vitest inside Tallpine (or official AO, or Claude Code)
// sessions whose environment carries TALLPINE_DATA_DIR, TALLPINE_SESSION_ID, CLAUDE_CODE_*
// and similar variables. Main-process code reads several of them, so an
// inherited value changes what a test exercises, or points it at the human's
// real ~/.tallpine. Mirrors backend/internal/testenv.
const SCRUB_PREFIXES = [
	"TALLPINE_",
	"VO_",
	"AO_",
	"CLAUDE",
	"ANTHROPIC_",
	"CODEX_",
	"OPENAI_",
	"TMUX",
	"GH_",
	"GITHUB_",
	"GITLAB_",
	"GIT_",
];
const SCRUB_NAMES = new Set(["HISTFILE", "ZDOTDIR", "BASH_ENV", "ENV", "PROMPT_COMMAND"]);

/** Deliberate opt-ins by the person running the tests, not session state. */
function keep(name: string): boolean {
	return ["TALLPINE_TEST_", "TALLPINE_LIVE_"].some((prefix) => name.startsWith(prefix));
}

export function isScrubbedEnvName(name: string): boolean {
	if (keep(name)) return false;
	return SCRUB_NAMES.has(name) || SCRUB_PREFIXES.some((prefix) => name.startsWith(prefix));
}

/**
 * Unsets inherited session variables and gives the test file a throwaway
 * HOME (and XDG dirs), so nothing a test runs can reach the human's home
 * directory or shell history. Removed again after the file's tests.
 */
export function applyHermeticTestEnv(): void {
	for (const name of Object.keys(process.env)) {
		if (isScrubbedEnvName(name)) delete process.env[name];
	}
	const home = mkdtempSync(join(tmpdir(), "tallpine-vitest-home-"));
	process.env.HOME = home;
	process.env.XDG_CONFIG_HOME = join(home, ".config");
	process.env.XDG_CACHE_HOME = join(home, ".cache");
	process.env.XDG_DATA_HOME = join(home, ".local", "share");
	process.env.XDG_STATE_HOME = join(home, ".local", "state");
	process.env.HISTFILE = "/dev/null";
	process.env.GIT_CONFIG_NOSYSTEM = "1";
	afterAll(() => rmSync(home, { recursive: true, force: true }));
}
