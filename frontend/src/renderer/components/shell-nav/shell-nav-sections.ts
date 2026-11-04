export type ShellNavSection = "home" | "sessions" | "automations" | "artifacts" | "backlog" | "inbox" | "terminals";

/** Pages that live on the icon rail; every other route is a Sessions page (boards, projects, sessions). */
const RAIL_PAGES: readonly [string, ShellNavSection][] = [
	["/automations", "automations"],
	["/artifacts", "artifacts"],
	["/backlog", "backlog"],
	["/inbox", "inbox"],
	["/terminals", "terminals"],
];

export function shellNavSection(pathname: string): ShellNavSection {
	if (pathname === "/") return "home";
	for (const [prefix, section] of RAIL_PAGES) {
		if (pathname === prefix || pathname.startsWith(`${prefix}/`)) return section;
	}
	return "sessions";
}
