/**
 * Main-process entry. Importing app-bootstrap first runs the env isolation,
 * app name and userData pin before any other module reads TALLPINE_*; the
 * register* calls then run in startup order and app-lifecycle goes last.
 */
import "./main/app-bootstrap";
import { registerAppRuntimeState } from "./main/app-runtime-state";
import { registerMainWindow } from "./main/window/main-window";
import { registerAppMenu } from "./main/window/app-menu";
import { registerShellEnvHost } from "./main/shell-env-host";
import { registerEditorIpc } from "./main/ipc/editor-ipc";
import { registerDaemonIpc } from "./main/ipc/daemon-ipc";
import { registerThemeIpc } from "./main/ipc/theme-ipc";
import { registerAppIpc } from "./main/ipc/app-ipc";
import { registerGitIpc } from "./main/ipc/git-ipc";
import { registerGithubIpc } from "./main/ipc/github-ipc";
import { registerNotificationsIpc } from "./main/ipc/notifications-ipc";
import { registerAppLifecycle } from "./main/app-lifecycle";

registerAppRuntimeState();
registerMainWindow();
registerAppMenu();
registerShellEnvHost();
registerEditorIpc();
registerDaemonIpc();
registerThemeIpc();
registerAppIpc();
registerGitIpc();
registerGithubIpc();
registerNotificationsIpc();
registerAppLifecycle();
