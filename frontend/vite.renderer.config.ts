// defineConfig comes from vitest/config (a superset of vite's) so the `test`
// block typechecks; vitest itself must be pointed at this file explicitly
// (package.json test script) because it only auto-discovers vite.config.*.
import { execFileSync } from "node:child_process";
import { defineConfig } from "vitest/config";
import type { Plugin } from "vite";
import { fileURLToPath, URL } from "node:url";
import { TanStackRouterVite } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { contentSecurityPolicy } from "./src/shared/renderer-csp";

function gitValue(args: string[], fallback: string): string {
	try {
		const value = execFileSync("git", args, {
			cwd: fileURLToPath(new URL("../", import.meta.url)),
			encoding: "utf8",
			stdio: ["ignore", "pipe", "ignore"],
		}).trim();
		return value || fallback;
	} catch {
		return fallback;
	}
}

function gitIsDirty(): boolean {
	try {
		return execFileSync("git", ["status", "--porcelain", "--untracked-files=all"], {
			cwd: fileURLToPath(new URL("../", import.meta.url)),
			encoding: "utf8",
			stdio: ["ignore", "pipe", "ignore"],
		}).trim().length > 0;
	} catch {
		return false;
	}
}

const DEV_BUILD_INFO = {
	branch: gitValue(["branch", "--show-current"], "main"),
	commit: gitValue(["rev-parse", "--short", "HEAD"], "unknown"),
	worktree: gitValue(["rev-parse", "--show-toplevel"], "unknown"),
	isDirty: gitIsDirty(),
};

export { contentSecurityPolicy };

const injectCspMeta: Plugin = {
	name: "inject-csp-meta",
	apply: "build",
	transformIndexHtml() {
		return [
			{
				tag: "meta",
				attrs: { "http-equiv": "Content-Security-Policy", content: contentSecurityPolicy("build") },
				injectTo: "head-prepend",
			},
		];
	},
};

const productUiReactBoundary: Plugin = {
	name: "product-ui-react-boundary",
	enforce: "pre",
	async resolveId(source, importer) {
		if (!importer?.includes("/packages/product-ui/")) {
			return null;
		}
		const remap =
			source === "react" ||
			source.startsWith("react/") ||
			source === "react-dom" ||
			source.startsWith("react-dom/") ||
			source === "motion" ||
			source.startsWith("motion/");
		if (!remap) {
			return null;
		}
		return this.resolve(
			source,
			fileURLToPath(new URL("./src/renderer/main.tsx", import.meta.url)),
			{ skipSelf: true },
		);
	},
};

// Test files (.ts) that need a DOM; see test.projects below.
const DOM_TS_TESTS: string[] = [
	"src/annotate-preload.test.ts",
	"src/renderer/__tests__/project-landing.test.ts",
	"src/renderer/components/CloneRepositoryDialog.test.ts",
	"src/renderer/components/Sidebar.surface.test.ts",
	"src/renderer/components/SessionsBoardAdapters.test.ts",
	"src/renderer/components/chat/WorkerUpdates.test.ts",
	"src/renderer/components/chat/activity-action.test.ts",
	"src/renderer/components/chat/useAutoOpenAssistantLinks.test.ts",
	"src/renderer/components/diffs/useCodeSelection.test.ts",
	"src/renderer/hooks/prefetchWorkspaceReview.test.ts",
	"src/renderer/hooks/useAgentModelsQuery.test.ts",
	"src/renderer/hooks/useCanResumeAgent.test.ts",
	"src/renderer/hooks/useDiffHighlight.test.ts",
	"src/renderer/hooks/useEditorHandoff.test.ts",
	"src/renderer/hooks/useFileAttachments.test.ts",
	"src/renderer/hooks/useHarnessUpdates.test.ts",
	"src/renderer/hooks/usePreparedClone.test.ts",
	"src/renderer/hooks/useProjectDefaultWorker.test.ts",
	"src/renderer/hooks/useResizable.test.ts",
	"src/renderer/hooks/useSessionMemory.test.ts",
	"src/renderer/hooks/useSessionSwitchSnap.test.ts",
	"src/renderer/hooks/useSessionUsageSummaries.test.ts",
	"src/renderer/hooks/useSessionWorkspaceFiles.test.ts",
	"src/renderer/hooks/useWorkspaceQuery.retained.test.ts",
	"src/renderer/lib/api-client.test.ts",
	"src/renderer/lib/backlog-chat-draft.test.ts",
	"src/renderer/lib/browser-overlay-sync.test.ts",
	"src/renderer/lib/chat-annotation-navigation.test.ts",
	"src/renderer/lib/chat-last-model.test.ts",
	"src/renderer/lib/command-palette.test.ts",
	"src/renderer/lib/daemon-tcp.test.ts",
	"src/renderer/lib/daemon-websocket.test.ts",
	"src/renderer/lib/event-stream-socket.test.ts",
	"src/renderer/lib/external-link-policy.test.ts",
	"src/renderer/lib/last-run-cue.test.ts",
	"src/renderer/lib/notifications.test.ts",
	"src/renderer/lib/open-shortcut-settings.test.ts",
	"src/renderer/lib/orchestrator-inspector-view.test.ts",
	"src/renderer/lib/platform.test.ts",
	"src/renderer/lib/remote-orchestrator.test.ts",
	"src/renderer/lib/render-frame.test.ts",
	"src/renderer/lib/report-problem.test.ts",
	"src/renderer/lib/restart-orchestrator.test.ts",
	"src/renderer/lib/session-reviews.test.ts",
	"src/renderer/lib/skills-offer.test.ts",
	"src/renderer/lib/stable-list.test.ts",
	"src/renderer/lib/terminal-mux.test.ts",
	"src/renderer/lib/theme.test.ts",
	"src/renderer/lib/window-translucency.test.ts",
	"src/renderer/stores/locale-store.test.ts",
	"src/renderer/stores/sidebar-order-store.test.ts",
	"src/renderer/stores/ui-store.test.ts",
	"src/renderer/test/bridge-parity.test.ts",
	"src/shared/annotation-trust.test.ts",
	"src/shared/browser-annotations.test.ts",
];

export default defineConfig({
	define: {
		"import.meta.env.VITE_TALLPINE_GIT_BRANCH": JSON.stringify(DEV_BUILD_INFO.branch),
		"import.meta.env.VITE_TALLPINE_GIT_COMMIT": JSON.stringify(DEV_BUILD_INFO.commit),
		"import.meta.env.VITE_TALLPINE_GIT_WORKTREE": JSON.stringify(DEV_BUILD_INFO.worktree),
		"import.meta.env.VITE_TALLPINE_GIT_DIRTY": JSON.stringify(String(DEV_BUILD_INFO.isDirty)),
	},
	// "@/" → the renderer root (src/renderer), the shadcn/ui import convention.
	resolve: {
		alias: {
			"@": fileURLToPath(new URL("./src/renderer", import.meta.url)),
			"@tallpine/shared": fileURLToPath(new URL("../packages/shared", import.meta.url)),
			"@tallpine/product-ui": fileURLToPath(
				new URL("../packages/product-ui/src/index.ts", import.meta.url),
			),
			// The alias above resolves product-ui to its source, so that package's
			// own imports resolve from packages/product-ui/ — which only has a
			// node_modules if `npm ci` was run there too. CI does that; a
			// frontend-only install does not, and the failure mode is quiet: every
			// test importing product-ui dies at transform time with "failed to
			// resolve clsx", which reads as pre-existing breakage rather than a
			// missing install. Point both runtime deps at the frontend copies so
			// one install is enough.
			clsx: fileURLToPath(new URL("./node_modules/clsx", import.meta.url)),
			"tailwind-merge": fileURLToPath(
				new URL("./node_modules/tailwind-merge", import.meta.url),
			),
		},
	},
	// Dev proxy for VITE_NO_ELECTRON=1 browser preview — forwards /api and /mux
	// to the daemon so the renderer can be tested against a running daemon from
	// a plain browser without an Electron shell.
	server: {
		proxy: {
			"/api": {
				target: process.env.TALLPINE_DEV_API_TARGET ?? "http://127.0.0.1:3101",
				changeOrigin: false,
			},
			"/mux": {
				target: process.env.TALLPINE_DEV_API_TARGET ?? "http://127.0.0.1:3101",
				changeOrigin: false,
				ws: true,
			},
		},
	},
	plugins: [
		TanStackRouterVite({
			routesDirectory: "./src/renderer/routes",
			generatedRouteTree: "./src/renderer/routeTree.gen.ts",
			target: "react",
			autoCodeSplitting: true,
		}),
		productUiReactBoundary,
		react(),
		tailwindcss(),
		injectCspMeta,
	],
	test: {
		// Vitest-only, so Forge builds of this config do not create a run dir.
		globalSetup: "./vitest.tmpdir.ts",
		testTimeout: 20_000,
		// Anchor node_modules at any depth: a bare "node_modules/**" replaces
		// vitest's default "**/node_modules/**" and only matches the root.
		// The docs site is its own package with its own Vitest config and
		// dependencies; the docs CI step runs its tests.
		exclude: ["**/node_modules/**", "dist/**", "dist-electron/**", "e2e/**", "src/docs/**"],
		globals: true,
		// Two projects: *.tsx and the named DOM .ts files run under jsdom with the
		// full DOM/RTL/i18n setup; every other test runs under node with a minimal
		// setup (jsdom costs ~0.3s of environment startup per file). A new .ts
		// test that touches window/document must be added to DOM_TS_TESTS.
		projects: [
			{
				extends: true,
				test: {
					name: "dom",
					environment: "jsdom",
					include: ["**/*.{test,spec}.?(c|m)[jt]sx", ...DOM_TS_TESTS],
					setupFiles: "./src/renderer/test/setup.ts",
				},
			},
			{
				extends: true,
				test: {
					name: "node",
					environment: "node",
					include: ["**/*.{test,spec}.?(c|m)[jt]s"],
					exclude: ["**/node_modules/**", "dist/**", "dist-electron/**", "e2e/**", "src/docs/**", ...DOM_TS_TESTS],
					setupFiles: "./src/renderer/test/setup.node.ts",
				},
			},
		],
	},
});
