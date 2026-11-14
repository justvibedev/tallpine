import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

export default defineConfig({
  resolve: {
    alias: {
      "@/": fileURLToPath(new URL("./src/", import.meta.url)),
      // The Lingui macros are compiled by the SWC plugin in the real build.
      // Tests only need the English source text, so a stub stands in.
      "@lingui/core/macro": fileURLToPath(new URL("./test/lingui-macro.ts", import.meta.url)),
    },
  },
  test: {
    environment: "jsdom",
    include: ["src/**/*.test.tsx", "scripts/**/*.test.ts"],
    // MDX compiles are CPU-bound; the 5s default timed out on a loaded machine (B717). Only a ceiling.
    testTimeout: 30_000,
  },
});
