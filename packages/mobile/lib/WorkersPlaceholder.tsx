import { useRouter } from "expo-router";
import { StyleSheet, View } from "react-native";
import { useBoardFailure } from "./useBoardFailure";
import { space } from "./tokens";
import { Button, EmptyState } from "./ui";

/** The Workers screen's loading and stalled states: always words, never a bare spinner. */
export function WorkersPlaceholder({ stalled, onRetry }: { stalled: boolean; onRetry: () => void }) {
	const router = useRouter();
	const failure = useBoardFailure();
	if (!stalled) {
		return <EmptyState icon="monitor-smartphone" pulse title="Connecting to your computer…" />;
	}
	return (
		<EmptyState
			icon={failure.icon}
			title={failure.title}
			message={failure.hint}
			action={
				<View style={styles.actions}>
					<Button title="Retry" icon="refresh-cw" variant="ghost" onPress={onRetry} />
					<Button title="Scan" icon="maximize" onPress={() => router.push("/pair")} />
				</View>
			}
		/>
	);
}

const styles = StyleSheet.create({
	actions: { flexDirection: "row", flexWrap: "wrap", justifyContent: "center", gap: space.sm, alignItems: "center" },
});
