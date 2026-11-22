import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import { defineMessageUnion } from "foldkit/message";
import { DraftInboxItem } from "@/chat/schemas";
import { addDraftInboxItem } from "@/lib/draft";
import { store } from "@/lib/effects";

// The Messages of the Tabs that attach an item must have the same two variants.
const AttachMessage = defineMessageUnion({
  AttachedItem: {},
  AttachFailed: { key: Schema.String, error: Schema.String },
});

export const AttachInboxItem = Command.define("AttachInboxItem", {
  args: { key: Schema.String, draftKey: Schema.String, item: DraftInboxItem },
  messages: [AttachMessage.AttachedItem, AttachMessage.AttachFailed],
  execute: ({ key, draftKey, item }) =>
    store(() => addDraftInboxItem(draftKey, item)).pipe(
      Effect.as(AttachMessage.AttachedItem()),
      Effect.catchTag("StoreFailed", (error) =>
        Effect.succeed(AttachMessage.AttachFailed({ key, error: error.message })),
      ),
    ),
});
