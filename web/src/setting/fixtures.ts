import { Message } from "./message";
import type { IssueSetting } from "./schemas";

export const harnessesLoaded = Message.HarnessesLoaded({ harnesses: [] });
export const saved = (setting: IssueSetting) => Message.Saved({ setting });
export const cleared = Message.Cleared();
export const closed = Message.ClickedClose();
export const loginRequired = Message.LoginRequired();
