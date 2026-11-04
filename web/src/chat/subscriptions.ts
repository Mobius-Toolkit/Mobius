import { Effect, Queue, Schema, Stream } from "effect";
import * as Port from "foldkit/port";
import * as Subscription from "foldkit/subscription";
import { listGitHubApps, type LiveEvents } from "@/api/api.gen";
import { onEvent, retryDelay } from "@/lib/events";
import { sameChat } from "@/lib/unread";
import { getVoiceSnapshot, registerVoiceInput, subscribeVoice } from "@/lib/voice";
import { inputId } from "./composer";
import { Message } from "./message";
import type { Model } from "./model";
import { ports } from "./ports";

const silenceLimit = 35_000;
const stopDelay = 500;

function emitVoice(queue: Queue.Enqueue<Message>) {
  const snap = getVoiceSnapshot();
  Queue.offerUnsafe(
    queue,
    Message.VoicePhaseChanged({
      phase: snap.phase,
      error: snap.error,
      canResend: snap.canResend,
    }),
  );
}

function chatInput() {
  return document.getElementById(inputId) as HTMLTextAreaElement | null;
}

type VoiceRegion = { lead: string; trail: string; value: string };

function createVoiceStream() {
  return Stream.callback<Message>(
    (queue) =>
      Effect.gen(function* () {
        // The text around the voice text of the run. An edit of the Owner in the field ends the region, and a render
        // that sets the Model value again must not end it.
        let region: VoiceRegion | undefined;
        const edited = (event: Event) => {
          if ((event.target as Element).id === inputId) {
            region = undefined;
          }
        };
        document.addEventListener("input", edited, true);

        const place = (
          target: HTMLTextAreaElement,
          voiceText: string,
          lead: string,
          trail: string,
        ) => {
          const value = lead + voiceText + trail;
          const cursor = lead.length + voiceText.length;
          // The height of the text up to the end of the voice text gives the scroll position. setSelectionRange does
          // not scroll a field without the focus, and WebKit on iOS does not scroll it reliably.
          target.value = lead + voiceText;
          const top = target.scrollHeight - target.clientHeight;
          target.value = value;
          target.setSelectionRange(cursor, cursor);
          target.scrollTop = top;
          region = { lead, trail, value: target.value };
          Queue.offerUnsafe(queue, Message.VoiceTextPlaced({ value, lead, trail }));
        };

        const placeAtCursor = (target: HTMLTextAreaElement, voiceText: string) => {
          const focused = document.activeElement === target;
          const start = focused ? target.selectionStart : target.value.length;
          const end = focused ? target.selectionEnd : target.value.length;
          const before = target.value.slice(0, start);
          const after = target.value.slice(end);
          const lead = before === "" || /\s$/.test(before) ? before : `${before} `;
          const trail = after === "" || /^\s/.test(after) ? after : ` ${after}`;
          place(target, voiceText, lead, trail);
        };

        emitVoice(queue);
        const unsubVoice = subscribeVoice(() => emitVoice(queue));
        const unregInput = registerVoiceInput({
          insert: (voiceText, live) => {
            const target = chatInput();
            if (!target) {
              return;
            }
            if (live && region) {
              place(target, voiceText, region.lead, region.trail);
            } else if (voiceText) {
              placeAtCursor(target, voiceText);
            }
          },
          insertLive: (voiceText, first) => {
            const target = chatInput();
            if (!target) {
              return false;
            }
            if (first) {
              placeAtCursor(target, voiceText);
              return true;
            }
            if (!region) {
              return false;
            }
            place(target, voiceText, region.lead, region.trail);
            return true;
          },
        });

        yield* Effect.addFinalizer(() =>
          Effect.sync(() => {
            unsubVoice();
            unregInput();
            document.removeEventListener("input", edited, true);
          }),
        );
      }),
    { bufferSize: 1000 },
  );
}

function createLiveEventsStream(target: {
  organization: string;
  repository: string;
  workstream: number;
}) {
  return Stream.callback<Message>(
    (queue) =>
      Effect.gen(function* () {
        let source: EventSource | null = null;
        let silenceTimer: ReturnType<typeof setTimeout> | undefined;
        let retryTimer: ReturnType<typeof setTimeout> | undefined;
        let stopped = false;

        const resetSilenceWatchdog = () => {
          clearTimeout(silenceTimer);
          silenceTimer = setTimeout(() => {
            reconnect();
          }, silenceLimit);
        };

        const reconnect = () => {
          if (stopped) return;
          if (source) {
            source.close();
          }
          connect();
        };

        const handleRetry = () => {
          listGitHubApps()
            .then((res) => {
              if (res.status === 401) {
                Queue.offerUnsafe(queue, Message.PageFailed({ error: "login required" }));
              } else if (!stopped) {
                reconnect();
              }
            })
            .catch(() => {
              if (!stopped) {
                reconnect();
              }
            });
        };

        const connect = () => {
          source = new EventSource("/api/events");
          resetSilenceWatchdog();

          source.addEventListener("open", () => {
            resetSilenceWatchdog();
            Queue.offerUnsafe(queue, Message.ConnectionOpened());
          });

          source.addEventListener("error", () => {
            if (source?.readyState === EventSource.CLOSED) {
              clearTimeout(retryTimer);
              retryTimer = setTimeout(handleRetry, retryDelay);
            }
          });

          onEvent<LiveEvents, "ping">(source, "ping", () => {
            resetSilenceWatchdog();
          });

          onEvent<LiveEvents, "message">(source, "message", (message) => {
            if (sameChat(message, target)) {
              Queue.offerUnsafe(queue, Message.ReceivedLiveMessage({ message }));
            }
          });

          onEvent<LiveEvents, "chat">(source, "chat", (state) => {
            if (sameChat(state, target)) {
              Queue.offerUnsafe(queue, Message.ReceivedLiveChatState({ state }));
            }
          });

          onEvent<LiveEvents, "action">(source, "action", (action) => {
            Queue.offerUnsafe(queue, Message.ReceivedLiveAction({ action }));
          });

          onEvent<LiveEvents, "question">(source, "question", (question) => {
            Queue.offerUnsafe(queue, Message.ReceivedLiveQuestion({ question }));
          });
        };

        const handleWake = () => {
          if (!document.hidden) {
            reconnect();
          }
        };

        const handleRestore = (event: PageTransitionEvent) => {
          if (event.persisted) {
            reconnect();
          }
        };

        window.addEventListener("online", handleWake);
        window.addEventListener("pageshow", handleRestore as EventListener);
        document.addEventListener("visibilitychange", handleWake);

        connect();

        yield* Effect.addFinalizer(() =>
          Effect.sync(() => {
            stopped = true;
            clearTimeout(silenceTimer);
            clearTimeout(retryTimer);
            window.removeEventListener("online", handleWake);
            window.removeEventListener("pageshow", handleRestore as EventListener);
            document.removeEventListener("visibilitychange", handleWake);
            if (source) {
              source.close();
            }
          }),
        );
      }),
    { bufferSize: 1000 },
  );
}

function createEscapeListener(queue: Queue.Enqueue<Message>) {
  return (e: KeyboardEvent) => {
    if (e.key === "Escape") {
      Queue.offerUnsafe(queue, Message.ClosePanel());
    }
  };
}

function createEscapeKeyStream() {
  return Stream.callback<Message>((queue) =>
    Effect.gen(function* () {
      const listener = createEscapeListener(queue);
      window.addEventListener("keydown", listener);
      yield* Effect.addFinalizer(() =>
        Effect.sync(() => {
          window.removeEventListener("keydown", listener);
        }),
      );
    }),
  );
}

export const subscriptions = Subscription.make<Model, Message>()((entry) => ({
  unreadPort: Port.subscriptionEntry(ports.inbound.unread, (unread) =>
    Message.ReceivedUnread({ unread }),
  ),
  briefPort: Port.subscriptionEntry(ports.inbound.brief, (brief) =>
    Message.ReceivedBrief({ brief }),
  ),
  trayItemsPort: Port.subscriptionEntry(ports.inbound.trayItems, (items) =>
    Message.ReceivedTrayItems({ items }),
  ),
  workstreamsPort: Port.subscriptionEntry(ports.inbound.workstreams, (workstreams) =>
    Message.ReceivedWorkstreams({ workstreams }),
  ),
  liveEvents: entry(
    {
      organization: Schema.String,
      repository: Schema.String,
      workstream: Schema.Number,
    },
    {
      modelToDependencies: (model) => ({
        organization: model.organization,
        repository: model.repository,
        workstream: model.workstream,
      }),
      dependenciesToStream: ({ organization, repository, workstream }) =>
        createLiveEventsStream({ organization, repository, workstream }),
    },
  ),
  voice: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () => createVoiceStream(),
    },
  ),
  stopReady: entry(
    { showStop: Schema.Boolean },
    {
      modelToDependencies: (model) => ({
        showStop:
          model.stoppable &&
          !model.draftText.trim() &&
          (model.draftImages as File[]).length === 0 &&
          (model.draftInboxItems as unknown[]).length === 0,
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
  escapeKey: entry(
    { hasActivePanel: Schema.Boolean },
    {
      modelToDependencies: (model) => ({ hasActivePanel: model.activePanel !== null }),
      dependenciesToStream: ({ hasActivePanel }) =>
        hasActivePanel ? createEscapeKeyStream() : Stream.empty,
    },
  ),
}));
