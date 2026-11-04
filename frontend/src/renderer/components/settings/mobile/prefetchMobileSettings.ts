import type { QueryClient } from "@tanstack/react-query";
import { fetchMobileStatus } from "../../../lib/mobile-api";
import { mobileStatusQueryKey } from "../../../lib/mobile-status-key";
import { fetchDevices, mobileDevicesQueryKey } from "../MobileDevicesSection";

/** Warm the Mobile page's pairing state, Keep awake and paired devices before it is first drawn. */
export function prefetchMobileSettings(queryClient: QueryClient): void {
	void queryClient.prefetchQuery({ queryKey: mobileStatusQueryKey, queryFn: fetchMobileStatus });
	void queryClient.prefetchQuery({ queryKey: mobileDevicesQueryKey, queryFn: fetchDevices });
}
