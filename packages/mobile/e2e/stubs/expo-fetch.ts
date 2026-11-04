// expo/fetch is the native session fetch; in node the (pin-aware) global one stands in.
export const fetch = (...args: Parameters<typeof globalThis.fetch>) => globalThis.fetch(...args);
