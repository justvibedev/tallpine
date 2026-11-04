/**
 * The one PR/MR URL parser for the UIs. Mirrors the backend's
 * domain.ParseChangeRequestURL path rule: the provider comes from the path
 * shape (/pull/N is GitHub, /-/merge_requests/N is GitLab), so github.com,
 * *.ghe.com, GHES and self-managed GitLab hosts all parse the same way.
 */
export interface ChangeRequestUrl {
	provider: "github" | "gitlab";
	/** Canonical URL without query, fragment or page-tab suffix. */
	url: string;
	repository: string;
	number: number;
}

const githubPath = /^\/([^/]+\/[^/]+)\/(?:pull|issues)\/(\d+)(?:\/.*)?$/;
const gitlabPath = /^\/(.+?)\/-\/merge_requests\/(\d+)(?:\/.*)?$/;

export function parseChangeRequestUrl(raw: string | undefined | null): ChangeRequestUrl | undefined {
	if (!raw) return undefined;
	let u: URL;
	try {
		u = new URL(raw.trim());
	} catch {
		return undefined;
	}
	if (u.protocol !== "https:" && u.protocol !== "http:") return undefined;
	const gl = gitlabPath.exec(u.pathname);
	if (gl) return build("gitlab", u, gl[1], gl[2], `/${gl[1]}/-/merge_requests/${gl[2]}`);
	const gh = githubPath.exec(u.pathname);
	if (gh) return build("github", u, gh[1], gh[2], `/${gh[1]}/pull/${gh[2]}`);
	return undefined;
}

function build(provider: "github" | "gitlab", u: URL, repository: string, num: string, path: string): ChangeRequestUrl {
	return { provider, url: `${u.protocol}//${u.host}${path}`, repository, number: Number(num) };
}
