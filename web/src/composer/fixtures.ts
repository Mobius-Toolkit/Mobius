import type { Queued } from "@/chat/schemas";
import { Message } from "./message";

export const draftTextChanged = (text: string) => Message.DraftTextChanged({ text });
export const inboxItemRemoved = (id: number | string) => Message.DraftInboxItemRemoved({ id });
export const saved = Message.CompletedSaveDraft();
export const sendSubmitted = Message.SendSubmitted();
export const queued = (item: Queued) => Message.MessageQueued({ item });
