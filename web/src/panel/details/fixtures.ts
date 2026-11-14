import type { Role } from "@/setting/schemas";
import { Message } from "./message";
import type { WorkstreamDetails } from "./model";

export const failed = Message.LoadFailed({ error: "none" });
export const rolesLoaded = Message.RolesLoaded({ roles: [] });
export const rolesGiven = (roles: readonly Role[]) => Message.RolesLoaded({ roles });
export const detailsLoaded = (details: WorkstreamDetails) => Message.DetailsLoaded({ details });
export const mcpServersLoaded = Message.McpServersLoaded({ servers: [] });
export const loginRequired = Message.LoginRequired();
