import { Effect, Option, Queue, Schema, Stream } from "effect";
import * as Subscription from "foldkit/subscription";
import { subscriptions as composerSubscriptions } from "@/composer/subscriptions";
import { currentOutboxVersion, subscribeOutbox } from "@/lib/outbox";
import { currentUnread, subscribeUnread, unreadCount } from "@/lib/unread";
import { currentWorkstreams, subscribeWorkstreams } from "@/lib/workstreams";
import { storeValues } from "@/live/stores";
import { Message, toGotAnswerComposerMessage, toGotComposerMessage } from "./message";
import type { Model } from "./model";

const stopDelay = 500;

function createEscapeKeyStream(message: Message) {
  return Stream.callback<Message>((queue) => {
    const listener = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        Queue.offerUnsafe(queue, message);
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

const escapeKind = (model: Model) =>
  model.activePanel !== null ? "panel" : model.todoList !== null ? "todos" : "none";

const chatDependencies = {
  organization: Schema.String,
  repository: Schema.String,
  workstream: Schema.Number,
};

const liftedAnswerComposer = Subscription.lift(composerSubscriptions)<Model, Message>({
  read: (model) => Option.fromNullishOr(model.answerComposer),
  toParentMessage: toGotAnswerComposerMessage,
});

// The Subscriptions of the rows of Inbox items and of their answer panel. The chat program and the Inbox program both
// have them, each with its own keys.
const itemEntries = Subscription.make<Model, Message>()((entry) => ({
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
  chatItemEscapeKey: entry(
    { open: Schema.Literals(["none", "panel", "todos"]) },
    {
      modelToDependencies: (model) => ({ open: escapeKind(model) }),
      dependenciesToStream: ({ open }) =>
        open === "none"
          ? Stream.empty
          : createEscapeKeyStream(
              open === "panel" ? Message.ClosePanel() : Message.CloseTodoList(),
            ),
    },
  ),
}));

const inboxItemEntries = Subscription.make<Model, Message>()((entry) => ({
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
  inboxItemEscapeKey: entry(
    { open: Schema.Literals(["none", "panel", "todos"]) },
    {
      modelToDependencies: (model) => ({ open: escapeKind(model) }),
      dependenciesToStream: ({ open }) =>
        open === "none"
          ? Stream.empty
          : createEscapeKeyStream(
              open === "panel" ? Message.ClosePanel() : Message.CloseTodoList(),
            ),
    },
  ),
}));

const itemSubscriptions = Subscription.aggregate(itemEntries, {
  chatItemAnswerDraft: liftedAnswerComposer.draft,
  chatItemAnswerVoice: liftedAnswerComposer.voice,
  chatItemAnswerVoiceClock: liftedAnswerComposer.voiceClock,
  chatItemAnswerVoiceInput: liftedAnswerComposer.voiceInput,
});

export const inboxItemSubscriptions = Subscription.aggregate(inboxItemEntries, {
  inboxItemAnswerDraft: liftedAnswerComposer.draft,
  inboxItemAnswerVoice: liftedAnswerComposer.voice,
  inboxItemAnswerVoiceClock: liftedAnswerComposer.voiceClock,
  inboxItemAnswerVoiceInput: liftedAnswerComposer.voiceInput,
});

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
          model.composer.draftInboxItems.length === 0 &&
          model.composer.draftSelections.length === 0,
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
