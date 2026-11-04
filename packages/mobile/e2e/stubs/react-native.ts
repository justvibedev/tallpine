// Just enough of react-native for modules the phone's api layer imports.
export const Platform = { OS: "ios", select: <T,>(o: { ios?: T; default?: T }) => o.ios ?? o.default };
export const AppState = { currentState: "active", addEventListener: () => ({ remove() {} }) };
export const NativeModules = {};
