// Shared default mocks for native modules that cannot load under node.
// Registered once through vitest.config.ts setupFiles; a test that needs
// different behaviour declares its own vi.mock for the module, which wins.
import { vi } from "vitest";

vi.mock("expo-secure-store", () => ({
	getItemAsync: vi.fn(),
	setItemAsync: vi.fn(),
	deleteItemAsync: vi.fn(),
}));
vi.mock("@react-native-async-storage/async-storage", () => ({
	default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() },
}));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
