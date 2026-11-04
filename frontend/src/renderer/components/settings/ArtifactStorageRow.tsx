import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { useUiStore } from "../../stores/ui-store";
import { Button } from "../ui/button";
import { SettingsRow } from "./SettingsRow";

/**
 * Settings > Storage entry for session artifacts. Sizes, stale state and
 * cleanup live in the Artifacts tab; this only points there.
 */
export function ArtifactStorageRow() {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const closeSettings = useUiStore((state) => state.closeSettings);
	return (
		<SettingsRow label={t("settings.artifactStorage.label")} description={t("settings.artifactStorage.description")}>
			<Button
				type="button"
				variant="footer"
				onClick={() => {
					closeSettings();
					void navigate({ to: "/artifacts" });
				}}
			>
				{t("settings.artifactStorage.review")}
			</Button>
		</SettingsRow>
	);
}
