import { defineConfig } from "vite";
import { fileURLToPath } from "node:url";

// Forge's VitePlugin handles all preload script build configuration.
export default defineConfig({
	resolve: { alias: { "@tallpine/shared": fileURLToPath(new URL("../packages/shared", import.meta.url)) } },
});
