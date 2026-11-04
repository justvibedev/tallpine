import { Platform } from "react-native";
import * as Device from "expo-device";
import { getInstallId } from "./installId";
import type { ExchangeDeps } from "./pairExchange";

/** The real network and storage for the pair exchange. Like connectRuntime.ts,
 * this glue depends on React Native's fetch and is exercised on a device. */
export function runtimeExchangeDeps(): ExchangeDeps {
	return {
		fetch: (url, init) => fetch(url, init),
		installId: getInstallId,
		deviceName: Device.deviceName ?? Device.modelName ?? Platform.OS,
	};
}
