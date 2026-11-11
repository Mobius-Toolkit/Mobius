import { Effect, Option, Queue, Schema, Stream } from "effect";
import * as Subscription from "foldkit/subscription";
import {
  draftValues,
  subscriptions as composerSubscriptions,
  voiceInput,
} from "@/composer/subscriptions";
import { answerDraftKey } from "@/lib/draft";
import { currentOutboxVersion, subscribeOutbox } from "@/lib/outbox";
import { currentUnread, subscribeUnread, unreadCount } from "@/lib/unread";
import { subscribeVoice, voiceSnapshot } from "@/lib/voice";
import { currentWorkstreams, subscribeWorkstreams } from "@/lib/workstreams";
import { storeValues } from "@/live/stores";
import { answerInputId } from "./composer";
import { Message, toGotComposerMessage } from "./message";
import type { Model } from "./model";

const stopDelay = 500;

export const voiceClock = Stream.callback<Message>((queue) => {
  const tick = () => Queue.offerUnsafe(queue, Message.VoiceTicked({ now: Date.now() }));
  return Effect.gen(function* () {
    tick();
    const timer = setInterval(tick, 1000);
    yield* Effect.addFinalizer(() =>
      Effect.sync(() => {
        clearInterval(timer);
      }),
    );
  });
});

function createEscapeKeyStream() {
  return Stream.callback<Message>((queue) => {
    const listener = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        Queue.offerUnsafe(queue, Message.ClosePanel());
      }
    };
    return Effect.gen(function* () {
      window.addEventListener("keydown", listener);
      yield* Effect.addFinalizer(() =>
        Effect.sync(() => {
          window.removeEventListener("keydown", listener);
        }),
      );
    });
  });
}

const chatDependencies = {
  organization: Schema.String,
  repository: Schema.String,
  workstream: Schema.Number,
};

// The Subscriptions of the rows of Inbox items and of their answer panel. The chat program and the Inbox program both
// have them.
export const itemSubscriptions = Subscription.make<Model, Message>()((entry) => ({
  chatItemWorkstreams: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () =>
        storeValues(subscribeWorkstreams, currentWorkstreams, (state) =>
          Message.ReceivedWorkstreams({ workstreams: state.list ?? null }),
        ),
    },
  ),
  chatItemAnswerDraft: entry(
    { id: Schema.NullOr(Schema.Number) },
    {
      modelToDependencies: (model) => ({
        id: model.activePanel?.type === "answer" ? model.activePanel.item.id : null,
      }),
      dependenciesToStream: ({ id }) =>
        id === null
          ? Stream.empty
          : draftValues(answerDraftKey(id), (draft) => Message.ReceivedAnswerDraft({ id, draft })),
    },
  ),
  chatItemVoice: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () =>
        storeValues(subscribeVoice, voiceSnapshot, (voice) => Message.VoiceChanged({ voice })),
    },
  ),
  chatItemVoiceClock: entry(
    { recording: Schema.Boolean },
    {
      modelToDependencies: (model) => ({ recording: model.voice.recording }),
      dependenciesToStream: ({ recording }) => (recording ? voiceClock : Stream.empty),
    },
  ),
  chatItemAnswerVoiceInput: entry(
    { id: Schema.NullOr(Schema.Number) },
    {
      modelToDependencies: (model) => ({
        id: model.activePanel?.type === "answer" ? model.activePanel.item.id : null,
      }),
      dependenciesToStream: ({ id }) =>
        id === null ? Stream.empty : voiceInput(answerDraftKey(id), answerInputId),
    },
  ),
  chatItemEscapeKey: entry(
    { hasActivePanel: Schema.Boolean },
    {
      modelToDependencies: (model) => ({ hasActivePanel: model.activePanel !== null }),
      dependenciesToStream: ({ hasActivePanel }) =>
        hasActivePanel ? createEscapeKeyStream() : Stream.empty,
    },
  ),
}));

export const inboxItemSubscriptions = Subscription.make<Model, Message>()((entry) => ({
  inboxItemWorkstreams: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () =>
        storeValues(subscribeWorkstreams, currentWorkstreams, (state) =>
          Message.ReceivedWorkstreams({ workstreams: state.list ?? null }),
        ),
    },
  ),
  inboxItemAnswerDraft: entry(
    { id: Schema.NullOr(Schema.Number) },
    {
      modelToDependencies: (model) => ({
        id: model.activePanel?.type === "answer" ? model.activePanel.item.id : null,
      }),
      dependenciesToStream: ({ id }) =>
        id === null
          ? Stream.empty
          : draftValues(answerDraftKey(id), (draft) => Message.ReceivedAnswerDraft({ id, draft })),
    },
  ),
  inboxItemVoice: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () =>
        storeValues(subscribeVoice, voiceSnapshot, (voice) => Message.VoiceChanged({ voice })),
    },
  ),
  inboxItemVoiceClock: entry(
    { recording: Schema.Boolean },
    {
      modelToDependencies: (model) => ({ recording: model.voice.recording }),
      dependenciesToStream: ({ recording }) => (recording ? voiceClock : Stream.empty),
    },
  ),
  inboxItemAnswerVoiceInput: entry(
    { id: Schema.NullOr(Schema.Number) },
    {
      modelToDependencies: (model) => ({
        id: model.activePanel?.type === "answer" ? model.activePanel.item.id : null,
      }),
      dependenciesToStream: ({ id }) =>
        id === null ? Stream.empty : voiceInput(answerDraftKey(id), answerInputId),
    },
  ),
  inboxItemEscapeKey: entry(
    { hasActivePanel: Schema.Boolean },
    {
      modelToDependencies: (model) => ({ hasActivePanel: model.activePanel !== null }),
      dependenciesToStream: ({ hasActivePanel }) =>
        hasActivePanel ? createEscapeKeyStream() : Stream.empty,
    },
  ),
}));

const chatSubscriptions = Subscription.make<Model, Message>()((entry) => ({
  chatUnread: entry(chatDependencies, {
    modelToDependencies: ({ organization, repository, workstream }) => ({
      organization,
      repository,
      workstream,
    }),
    dependenciesToStream: (chat) =>
      storeValues(subscribeUnread, currentUnread, (list) =>
        Message.ReceivedUnread({ unread: list === undefined ? null : unreadCount(list, chat) }),
      ),
  }),
  chatOutbox: entry(chatDependencies, {
    modelToDependencies: ({ organization, repository, workstream }) => ({
      organization,
      repository,
      workstream,
    }),
    dependenciesToStream: () =>
      storeValues(subscribeOutbox, currentOutboxVersion, () => Message.OutboxChanged()),
  }),
  chatStopReady: entry(
    { showStop: Schema.Boolean },
    {
      modelToDependencies: (model) => ({
        showStop:
          model.stoppable &&
          !model.composer.draftText.trim() &&
          model.composer.draftImages.length === 0 &&
          model.composer.draftInboxItems.length === 0,
      }),
      // A tap on Send can land on the Stop button that replaces it. Thus Stop takes no tap in its first moments.
      dependenciesToStream: ({ showStop }) =>
        Stream.concat(
          Stream.make(Message.StopReadyChanged({ ready: false })),
          showStop
            ? Stream.fromEffect(
                Effect.sleep(stopDelay).pipe(Effect.as(Message.StopReadyChanged({ ready: true }))),
              )
            : Stream.empty,
        ),
    },
  ),
}));

const liftedComposer = Subscription.lift(composerSubscriptions)<Model, Message>({
  read: (model) => Option.some(model.composer),
  toParentMessage: toGotComposerMessage,
});

export const subscriptions = Subscription.aggregate(
  itemSubscriptions,
  chatSubscriptions,
  liftedComposer,
);
