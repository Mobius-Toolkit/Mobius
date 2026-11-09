import { Message } from "./message";

export const failed = Message.LoadFailed({ error: "none" });
export const rolesLoaded = Message.RolesLoaded({ roles: [] });
export const mcpServersLoaded = Message.McpServersLoaded({ servers: [] });
export const loginRequired = Message.LoginRequired();
