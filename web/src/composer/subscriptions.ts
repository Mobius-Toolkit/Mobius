import { Effect, Queue, Schema, Stream } from "effect";
import * as Subscription from "foldkit/subscription";
import {
  currentDraft,
  type Draft as StoredDraft,
  loadDraft,
  subscribeDrafts,
  updateDraft,
} from "@/lib/draft";
import { store } from "@/lib/effects";
import { insertVoiceText, registerVoiceInput, subscribeVoice, voiceSnapshot } from "@/lib/voice";
import { storeValues } from "@/live/stores";
import { Message } from "./message";
import { inputId, type Model } from "./model";
import type { Draft } from "@/chat/schemas";

export function draftValues<Msg = Message>(key: string, toMessage: (draft: Draft) => Msg) {
  return Stream.callback<Msg>((queue) =>
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

export function voiceInput<Msg = Message>(key: string, fieldId: string) {
  return Stream.callback<Msg>(() =>
    Effect.gen(function* () {
      const unregister = registerVoiceInput(key, fieldId, insertAtCursor(key, fieldId));
      yield* Effect.addFinalizer(() =>
        Effect.sync(() => {
          unregister();
        }),
      );
    }),
  );
}

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

export const subscriptions = Subscription.make<Model, Message>()((entry) => ({
  draft: entry(
    { key: Schema.String },
    {
      modelToDependencies: (model) => ({ key: model.draftKey }),
      dependenciesToStream: ({ key }) =>
        draftValues(key, (draft) => Message.ReceivedDraft({ draft })),
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
  voiceInput: entry(
    { key: Schema.String, fieldId: Schema.String },
    {
      modelToDependencies: (model) => ({
        key: model.draftKey,
        fieldId: inputId(model.instance),
      }),
      dependenciesToStream: ({ key, fieldId }) => voiceInput(key, fieldId),
    },
  ),
}));
