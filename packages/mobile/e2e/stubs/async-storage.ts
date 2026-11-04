// In-memory AsyncStorage for the node e2e (the real one needs the RN runtime).
const store = new Map<string, string>();
const AsyncStorage = {
	getItem: async (key: string) => store.get(key) ?? null,
	setItem: async (key: string, value: string) => void store.set(key, value),
	removeItem: async (key: string) => void store.delete(key),
	multiGet: async (keys: string[]) => keys.map((k) => [k, store.get(k) ?? null] as [string, string | null]),
	multiSet: async (pairs: [string, string][]) => pairs.forEach(([k, v]) => store.set(k, v)),
	multiRemove: async (keys: string[]) => keys.forEach((k) => store.delete(k)),
	getAllKeys: async () => [...store.keys()],
	clear: async () => store.clear(),
};
export default AsyncStorage;
