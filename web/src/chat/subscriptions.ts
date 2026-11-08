import { Effect, Queue, Schema, Stream } from "effect";
import * as Port from "foldkit/port";
import * as Subscription from "foldkit/subscription";
import {
  answerDraftKey,
  chatDraftKey,
  currentDraft,
  type Draft as StoredDraft,
  loadDraft,
  subscribeDrafts,
  updateDraft,
} from "@/lib/draft";
import { store } from "@/lib/effects";
import { currentUnread, subscribeUnread, unreadCount } from "@/lib/unread";
import { sameChat } from "@/lib/unread";
import { insertVoiceText, registerVoiceInput, subscribeVoice, voiceSnapshot } from "@/lib/voice";
import { currentWorkstreams, subscribeWorkstreams } from "@/lib/workstreams";
import { liveEvents, liveListener } from "@/live/events";
import { storeValues } from "@/live/stores";
import { answerInputId, inputId } from "./composer";
import { Message } from "./message";
import type { Model } from "./model";
import { ports } from "./ports";
import { ChatMessage, ChatState, type Draft, InboxItem, PendingAction } from "./schemas";

const stopDelay = 500;

// Gives the draft of the key to the program: first the draft that the browser store holds, then each change.
function draftValues(key: string, toMessage: (draft: Draft) => Message) {
  return Stream.callback<Message>((queue) =>
    Effect.gen(function* () {
      let last: StoredDraft | undefined;
      const emit = () => {
        const draft = currentDraft(key);
        if (draft !== undefined && draft !== last) {
          last = draft;
          Queue.offerUnsafe(queue, toMessage(draft));
        }
      };
      const unsubscribe = subscribeDrafts(emit);
      yield* Effect.addFinalizer(() =>
        Effect.sync(() => {
          unsubscribe();
        }),
      );
      yield* store(() => loadDraft(key)).pipe(Effect.ignore);
      emit();
    }),
  );
}

// The text of a conversion goes to the cursor of the field. The height of the text up to the end of the voice text
// gives the scroll position, because setSelectionRange does not scroll a field without the focus and WebKit on iOS does
// not scroll it reliably.
function insertAtCursor(key: string, fieldId: string) {
  return (voiceText: string) => {
    const field = document.getElementById(fieldId);
    if (!(field instanceof HTMLTextAreaElement)) {
      return;
    }
    const focused = document.activeElement === field;
    const start = focused ? field.selectionStart : field.value.length;
    const end = focused ? field.selectionEnd : field.value.length;
    const { text, cursor } = insertVoiceText(field.value, voiceText, start, end);
    void updateDraft(key, (draft) => ({ ...draft, text }));
    field.value = text.slice(0, cursor);
    const top = field.scrollHeight - field.clientHeight;
    field.value = text;
    field.setSelectionRange(cursor, cursor);
    field.scrollTop = top;
  };
}

// The recording stops when no mounted input has its draft key, so each field has its own Subscription: a change of the
// answer panel must not unmount the input of the chat.
function voiceInput(key: string, fieldId: string) {
  return Stream.callback<Message>(() =>
    Effect.gen(function* () {
      const unregister = registerVoiceInput(key, insertAtCursor(key, fieldId));
      yield* Effect.addFinalizer(() =>
        Effect.sync(() => {
          unregister();
        }),
      );
    }),
  );
}

const voiceClock = Stream.callback<Message>((queue) => {
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
  trayItemsPort: Port.subscriptionEntry(ports.inbound.trayItems, (items) =>
    Message.ReceivedTrayItems({ items }),
  ),
  workstreams: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () =>
        storeValues(subscribeWorkstreams, currentWorkstreams, (state) =>
          Message.ReceivedWorkstreams({ workstreams: state.list ?? null }),
        ),
    },
  ),
  answerDraft: entry(
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
  voice: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () =>
        storeValues(subscribeVoice, voiceSnapshot, (voice) => Message.VoiceChanged({ voice })),
    },
  ),
  voiceClock: entry(
    { recording: Schema.Boolean },
    {
      modelToDependencies: (model) => ({ recording: model.voice.recording }),
      dependenciesToStream: ({ recording }) => (recording ? voiceClock : Stream.empty),
    },
  ),
  answerVoiceInput: entry(
    { id: Schema.NullOr(Schema.Number) },
    {
      modelToDependencies: (model) => ({
        id: model.activePanel?.type === "answer" ? model.activePanel.item.id : null,
      }),
      dependenciesToStream: ({ id }) =>
        id === null ? Stream.empty : voiceInput(answerDraftKey(id), answerInputId),
    },
  ),
  escapeKey: entry(
    { hasActivePanel: Schema.Boolean },
    {
      modelToDependencies: (model) => ({ hasActivePanel: model.activePanel !== null }),
      dependenciesToStream: ({ hasActivePanel }) =>
        hasActivePanel ? createEscapeKeyStream() : Stream.empty,
    },
  ),
}));

const chatSubscriptions = Subscription.make<Model, Message>()((entry) => ({
  briefPort: Port.subscriptionEntry(ports.inbound.brief, (brief) =>
    Message.ReceivedBrief({ brief }),
  ),
  unread: entry(chatDependencies, {
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
  chatDraft: entry(
    { key: Schema.String },
    {
      modelToDependencies: ({ organization, repository, workstream }) => ({
        key: chatDraftKey(organization, repository, workstream),
      }),
      dependenciesToStream: ({ key }) =>
        draftValues(key, (draft) => Message.ReceivedDraft({ draft })),
    },
  ),
  liveEvents: entry(chatDependencies, {
    modelToDependencies: ({ organization, repository, workstream }) => ({
      organization,
      repository,
      workstream,
    }),
    dependenciesToStream: (chat) =>
      liveEvents<Message>({
        opened: Message.ConnectionOpened(),
        loginRequired: Message.PageFailed({ error: "login required" }),
        listeners: [
          liveListener("message", ChatMessage, (message) =>
            sameChat(message, chat) ? Message.ReceivedLiveMessage({ message }) : null,
          ),
          liveListener("chat", ChatState, (state) =>
            sameChat(state, chat) ? Message.ReceivedLiveChatState({ state }) : null,
          ),
          liveListener("action", PendingAction, (action) => Message.ReceivedLiveAction({ action })),
          liveListener("question", InboxItem, (question) =>
            Message.ReceivedLiveQuestion({ question }),
          ),
        ],
      }),
  }),
  chatVoiceInput: entry(
    { key: Schema.String },
    {
      modelToDependencies: ({ organization, repository, workstream }) => ({
        key: chatDraftKey(organization, repository, workstream),
      }),
      dependenciesToStream: ({ key }) => voiceInput(key, inputId),
    },
  ),
  stopReady: entry(
    { showStop: Schema.Boolean },
    {
      modelToDependencies: (model) => ({
        showStop:
          model.stoppable &&
          !model.draftText.trim() &&
          model.draftImages.length === 0 &&
          model.draftInboxItems.length === 0,
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

export const subscriptions = Subscription.aggregate(itemSubscriptions, chatSubscriptions);
