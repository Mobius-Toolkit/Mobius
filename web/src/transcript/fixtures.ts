import { Message } from "./message";

export const closed = Message.ClickedBack();
export const loginRequired = Message.LoginRequired();
export const loaded = Message.Loaded({ lines: [], older: false, initial: true });
