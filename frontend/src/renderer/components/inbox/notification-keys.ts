import { refKey, type HostId } from "../../lib/hosts";

export const notificationKey = (hostId: HostId, id: string) => refKey({ host: hostId, id });
