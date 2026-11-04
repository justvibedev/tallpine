import { Tabs } from "expo-router";
import { SidebarNavigationShell } from "../../lib/sidebar-navigation-shell";

// expo-router 58 drops the initialRouteName prop on <Tabs>; route config lives here.
export const unstable_settings = { initialRouteName: "index" };

export default function SidebarLayout() {
	return (
		<SidebarNavigationShell>
			{/* Sidebar destinations are peers. Replacing stack screens destroys
			    their lists during drawer closing; retain visited tabs instead.
			    The existing sidebar remains the only tab-selection UI. */}
			<Tabs
				backBehavior="none"
				tabBar={() => null}
				detachInactiveScreens
				screenOptions={{ headerShown: false, animation: "none", lazy: true, freezeOnBlur: true }}
			>
				{/* expo-router 58 renders only screens the layout declares; an
				    undeclared route file stays registered but draws nothing (a
				    blank page). Declare every file in this folder. */}
				<Tabs.Screen name="index" />
				<Tabs.Screen name="projects" />
				<Tabs.Screen name="prs" />
			</Tabs>
		</SidebarNavigationShell>
	);
}
