import type { NextSetting } from "@/setting/schemas";
import { Message } from "./message";
import type { TaskLine } from "./model";

export const loaded = Message.TasksLoaded({ lines: [] });
export const linesLoaded = (lines: readonly TaskLine[]) => Message.TasksLoaded({ lines });
export const fallbackLoaded = (fallback: NextSetting) => Message.FallbackLoaded({ fallback });
export const failed = Message.LoadFailed({ error: "no fallback" });
export const attached = Message.AttachedTask();
export const loginRequired = Message.LoginRequired();
