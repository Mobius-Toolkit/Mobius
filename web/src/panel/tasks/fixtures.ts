import { Message } from "./message";

export const loaded = Message.TasksLoaded({ lines: [] });
export const failed = Message.LoadFailed({ error: "no fallback" });
export const loginRequired = Message.LoginRequired();
