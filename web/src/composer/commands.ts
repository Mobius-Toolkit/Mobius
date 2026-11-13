import { Effect, Schema } from "effect";
import * as Command from "foldkit/command";
import {
  type Draft as StoredDraft,
  type DraftInboxItem,
  type DraftVoice,
  updateDraft,
} from "@/lib/draft";
import { fitted, store } from "@/lib/effects";
import { fitImage, tooManyImages } from "@/lib/images";
import { addQueued, newMessageId } from "@/lib/outbox";
import { resend, stopVoice, toggleVoice } from "@/lib/voice";
import { Message } from "./message";
import { DraftInboxItems, Images, Queued } from "@/chat/schemas";

const clearDraft = (key: string) =>
  store(() =>
    updateDraft(key, (draft) => ({ ...draft, text: "", images: [], inboxItems: [], voice: null })),
  ).pipe(Effect.ignore);

const readyVoice: DraftVoice = { state: "ready", audio: null, conversion: "", error: "" };

const restoreDraft = (
  key: string,
  sent: {
    text: string;
    images: File[];
    inboxItems: DraftInboxItem[];
    voice: boolean;
  },
) =>
  store(() =>
    updateDraft(key, (draft) => ({
      text:
        draft.text === sent.text ? sent.text : [sent.text, draft.text].filter(Boolean).join(" "),
      images: sent.images,
      inboxItems: [
        ...sent.inboxItems,
        ...draft.inboxItems.filter((item) => !sent.inboxItems.some(({ id }) => id === item.id)),
      ],
      voice: draft.voice ?? (sent.voice ? readyVoice : null),
    })),
  ).pipe(Effect.ignore);

const savedDraft = (key: string, change: (draft: StoredDraft) => StoredDraft) =>
  store(() => updateDraft(key, change)).pipe(
    Effect.ignore,
    Effect.as(Message.CompletedSaveDraft()),
  );

export const SaveDraftText = Command.define("SaveDraftText", {
  args: { key: Schema.String, text: Schema.String },
  messages: [Message.CompletedSaveDraft],
  execute: ({ key, text }) => savedDraft(key, (draft) => ({ ...draft, text })),
});

export const SaveDraftImages = Command.define("SaveDraftImages", {
  args: { key: Schema.String, images: Images },
  messages: [Message.CompletedSaveDraft],
  execute: ({ key, images }) => savedDraft(key, (draft) => ({ ...draft, images })),
});

export const SaveDraftInboxItems = Command.define("SaveDraftInboxItems", {
  args: { key: Schema.String, inboxItems: DraftInboxItems },
  messages: [Message.CompletedSaveDraft],
  execute: ({ key, inboxItems }) => savedDraft(key, (draft) => ({ ...draft, inboxItems })),
});

export const FitImages = Command.define("FitImages", {
  args: { files: Images, room: Schema.Number },
  messages: [Message.ImagesFitted],
  execute: ({ files, room }) =>
    Effect.gen(function* () {
      let problem = "";
      const added: File[] = [];
      for (const file of files.slice(0, Math.max(0, room))) {
        yield* fitted(() => fitImage(file)).pipe(
          Effect.match({
            onSuccess: (image) => {
              added.push(image);
            },
            onFailure: (error) => {
              problem = error.message;
            },
          }),
        );
      }
      if (!problem && files.length > room) {
        problem = tooManyImages;
      }
      return Message.ImagesFitted({ images: added, problem });
    }),
});

export const ClickAttach = Command.define("ClickAttach", {
  args: { pickerId: Schema.String },
  messages: [Message.CompletedClickAttach],
  execute: ({ pickerId }) =>
    Effect.sync(() => {
      document.getElementById(pickerId)?.click();
      return Message.CompletedClickAttach();
    }),
});

export const ToggleVoice = Command.define("ToggleVoice", {
  args: { key: Schema.String, fieldId: Schema.String },
  messages: [Message.CompletedToggleVoice],
  execute: ({ key, fieldId }) =>
    Effect.sync(() => {
      toggleVoice(key, fieldId);
      return Message.CompletedToggleVoice();
    }),
});

export const StopVoice = Command.define("StopVoice", {
  messages: [Message.CompletedStopVoice],
  execute: Effect.sync(() => {
    stopVoice();
    return Message.CompletedStopVoice();
  }),
});

export const ResendVoice = Command.define("ResendVoice", {
  args: { key: Schema.String, fieldId: Schema.String },
  messages: [Message.CompletedResendVoice],
  execute: ({ key, fieldId }) =>
    store(() => resend(key, fieldId)).pipe(
      Effect.ignore,
      Effect.as(Message.CompletedResendVoice()),
    ),
});

export const RemoveVoice = Command.define("RemoveVoice", {
  args: { key: Schema.String },
  messages: [Message.CompletedRemoveVoice],
  execute: ({ key }) =>
    savedDraft(key, (draft) => ({ ...draft, voice: null })).pipe(
      Effect.as(Message.CompletedRemoveVoice()),
    ),
});

export const EnqueueMessage = Command.define("EnqueueMessage", {
  args: {
    organization: Schema.String,
    repository: Schema.String,
    workstream: Schema.Number,
    key: Schema.String,
    text: Schema.String,
    images: Images,
    inboxItems: DraftInboxItems,
    voice: Schema.Boolean,
  },
  messages: [Message.MessageQueued, Message.MessageRefused],
  execute: ({ organization, repository, workstream, key, text, images, inboxItems, voice }) =>
    Effect.gen(function* () {
      const item: Queued = {
        id: newMessageId(),
        organization,
        repository,
        workstream,
        text,
        images,
        inboxItems: inboxItems.map(({ reference, title, reason, link }) => ({
          reference,
          title,
          reason,
          link,
        })),
        voice,
        time: new Date().toISOString(),
      };
      yield* clearDraft(key);
      yield* store(() => addQueued(item));
      return Message.MessageQueued({ item });
    }).pipe(
      Effect.catchTag("StoreFailed", (error) =>
        restoreDraft(key, { text, images, inboxItems, voice }).pipe(
          Effect.as(Message.MessageRefused({ error: error.message })),
        ),
      ),
    ),
});
