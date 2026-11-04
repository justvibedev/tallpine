import { ProjectSecurityModeSwitch } from "../settings/ProjectSecurityModeSwitch";

/** Security: this project's Security mode and Docker opt-in. */
export function ProjectSecuritySection({ projectId }: { projectId: string }) {
	return <ProjectSecurityModeSwitch projectId={projectId} />;
}
