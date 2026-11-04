import { fileURLToPath } from "node:url";

const stub = (name: string) => fileURLToPath(new URL(`./e2e/stubs/${name}`, import.meta.url));

// Plain object, not defineConfig: importing vitest/config here drags vite's fetch
// types into `tsc` and breaks lib/pinnedFetch.ts.
// Phone e2e: the phone's real connect/pair/api code in node against a real
// daemon built into $TMPDIR. Only the React Native runtime (storage, native
// pinning, fetch) is replaced; see e2e/setup.ts.
export default {
	resolve: {
		alias: {
			"@tallpine/shared": fileURLToPath(new URL("../shared", import.meta.url)),
			"@tallpine/product-ui": fileURLToPath(new URL("../product-ui/src", import.meta.url)),
			"@react-native-async-storage/async-storage": stub("async-storage.ts"),
			"expo-secure-store": stub("secure-store.ts"),
			"react-native": stub("react-native.ts"),
			"expo/fetch": stub("expo-fetch.ts"),
		},
	},
	test: {
		environment: "node",
		include: ["e2e/**/*.e2e.ts"],
		globalSetup: ["e2e/global.ts"],
		setupFiles: ["e2e/setup.ts"],
		testTimeout: 60_000,
		hookTimeout: 240_000, // includes the go build of the daemon
		fileParallelism: false,
	},
};
