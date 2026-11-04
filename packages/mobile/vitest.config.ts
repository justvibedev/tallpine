import { fileURLToPath } from "node:url";

// Plain object, not defineConfig: see vitest.e2e.config.ts.
export default {
	test: { setupFiles: ["./test/mocks.ts"] },
	resolve: {
		alias: {
			"@tallpine/shared": fileURLToPath(new URL("../shared", import.meta.url)),
			"@tallpine/product-ui": fileURLToPath(new URL("../product-ui/src", import.meta.url)),
		},
	},
};
