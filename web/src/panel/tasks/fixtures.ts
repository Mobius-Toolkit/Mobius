import type { NextSetting } from "@/setting/schemas";
import { Message } from "./message";
import type { TaskLine } from "./model";

export const loaded = Message.TasksLoaded({ lines: [] });
export const linesLoaded = (lines: readonly TaskLine[]) => Message.TasksLoaded({ lines });
export const fallbackLoaded = (fallback: NextSetting) => Message.FallbackLoaded({ fallback });
export const failed = Message.LoadFailed({ error: "no fallback" });
export const attached = Message.AttachedTask();
export const loginRequired = Message.LoginRequired();
export const opened = (line: TaskLine) => Message.OpenedTask({ line, scroll: 0 });
export const scrollRestored = Message.CompletedRestoreScroll();
export const detailFailed = (line: TaskLine) =>
  Message.TaskDetailFailed({
    repository: line.repository,
    number: line.number,
    error: "no detail",
  });
