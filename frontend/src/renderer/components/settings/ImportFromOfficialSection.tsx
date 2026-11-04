import { Import } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "../ui/button";
import { ImportFromOfficialDialog } from "./ImportFromOfficialDialog";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

/** Settings entry for migrating projects from an official Agent Orchestrator install. */
export function ImportFromOfficialSection({ titleHidden }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	return (
		<>
			<SettingsSection grouped sectionId="importAo" title={t("settings.importAo.title")} {...(titleHidden ? { titleHidden } : {})}>
				<SettingsRow label={t("settings.importAo.rowLabel")}>
					<Button
						className="text-settings-muted hover:text-settings-label"
						onClick={() => setOpen(true)}
						size="sm"
						type="button"
						variant="ghost"
					>
						<Import aria-hidden="true" className="size-icon-base" />
						{t("settings.importAo.action")}
					</Button>
				</SettingsRow>
			</SettingsSection>
			<ImportFromOfficialDialog onOpenChange={setOpen} open={open} />
		</>
	);
}
