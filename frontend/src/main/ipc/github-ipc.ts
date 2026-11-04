/* Split out of main.ts (B297, P52). */
declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;
declare const __TALLPINE_TEAM_ID__: string | undefined;
declare const __TALLPINE_BUILD_INFO__: { version: string; commit: string; date: string; dirty: string } | undefined;
import { execFileAsync } from "../app-bootstrap";
import { ctx } from "../app-context";
import { ipcMain } from "../app-runtime-state";
import { daemonEnv } from "../daemon/daemon-lifecycle";
import { ensureShellEnv } from "../shell-env-host";


export type GitHubOwner = { login: string; avatarUrl: string };


export async function refreshGitHubOwners(): Promise<GitHubOwner[]> {
	await ensureShellEnv();
	try {
		const { stdout } = await execFileAsync("gh", ["api", "user", "--jq", "[.login, .avatar_url] | @tsv"], {
			env: daemonEnv(),
			timeout: 5000,
		});
		let organizationOutput = "";
		try {
			({ stdout: organizationOutput } = await execFileAsync("gh", ["api", "user/memberships/orgs", "--paginate", "--jq", ".[] | [.organization.login, .organization.avatar_url] | @tsv"], {
				env: daemonEnv(),
				timeout: 8000,
			}));
		} catch {
			// The authenticated account may not have the read:org scope; the personal owner is still usable.
		}
		const owners = [stdout, ...organizationOutput.split("\n")].map((line) => {
			const [login, avatarUrl] = line.trim().split("\t");
			return login && avatarUrl ? { login, avatarUrl } : null;
		}).filter((owner): owner is GitHubOwner => owner !== null);
		ctx.cachedGitHubOwners = [...new Map(owners.map((owner) => [owner.login, owner])).values()];
		return ctx.cachedGitHubOwners;
	} catch {
		return ctx.cachedGitHubOwners;
	}
}

export function registerGithubIpc(): void {

	ipcMain.handle("app:getGitHubLogin", async (_event, repoPath?: string) => {
		await ensureShellEnv();
		const gitConfig = async (args: string[]) => {
			try {
				const { stdout } = await execFileAsync("git", args, { env: daemonEnv(), timeout: 3000 });
				return stdout.trim();
			} catch {
				return "";
			}
		};
		const candidates = [
			typeof repoPath === "string" && repoPath.trim() ? await gitConfig(["-C", repoPath.trim(), "config", "--get", "github.user"]) : "",
			await gitConfig(["config", "--global", "--get", "github.user"]),
			process.env.TALLPINE_GITHUB_LOGIN?.trim() ?? "",
		];
		try {
			const { stdout } = await execFileAsync("gh", ["api", "user", "--jq", ".login"], {
				env: daemonEnv(),
				timeout: 5000,
			});
			candidates.push(stdout.trim());
		} catch {
			// GitHub CLI may not be installed or authenticated yet; keep the editable fallback.
		}
		const gitNames = [
			typeof repoPath === "string" && repoPath.trim() ? await gitConfig(["-C", repoPath.trim(), "config", "--get", "user.name"]) : "",
			await gitConfig(["config", "--global", "--get", "user.name"]),
		].filter((candidate) => /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$/.test(candidate));
		candidates.push(...gitNames);
		return candidates.find((candidate) => candidate.length > 0) ?? "";
	});


	ipcMain.handle("app:getCachedGitHubOwners", () => ctx.cachedGitHubOwners);

	ipcMain.handle("app:refreshGitHubOwners", () => refreshGitHubOwners());

	ipcMain.handle("app:checkGitHubRepositoryAvailability", async (_event, input: { owner: string; name: string }) => {
		await ensureShellEnv();
		const owner = input.owner.trim();
		const name = input.name.trim();
		if (!owner || !name) {
			return { available: false, message: "Owner and repository name are required." };
		}
		try {
			await execFileAsync("gh", ["api", `repos/${owner}/${name}`], {
				env: daemonEnv(),
				timeout: 8000,
			});
			return { available: false, message: "Repository name is already in use for this owner." };
		} catch (error) {
			const output = error instanceof Error ? error.message : String(error);
			if (/404|not found/i.test(output)) {
				return { available: true };
			}
			return { available: false, message: "Could not check this repository name. Confirm GitHub CLI is signed in." };
		}
	});
}
