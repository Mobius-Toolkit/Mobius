import { Message } from "./message";

export const loaded = Message.TasksLoaded({ lines: [] });
export const failed = Message.LoadFailed({ error: "no fallback" });
export const attached = Message.AttachedTask();
export const loginRequired = Message.LoginRequired();
