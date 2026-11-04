import { Effect, Queue, Schema, Stream } from "effect";
import * as Port from "foldkit/port";
import * as Subscription from "foldkit/subscription";
import { listGitHubApps, type LiveEvents } from "@/api/api.gen";
import { onEvent, retryDelay } from "@/lib/events";
import { sameChat } from "@/lib/unread";
import { getVoiceSnapshot, registerVoiceInput, subscribeVoice } from "@/lib/voice";
import { Message } from "./message";
import type { Model } from "./model";
import { ports } from "./ports";

const silenceLimit = 35_000;

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

function createVoiceStream() {
  return Stream.callback<Message>(
    (queue) =>
      Effect.gen(function* () {
        emitVoice(queue);
        const unsubVoice = subscribeVoice(() => emitVoice(queue));
        const unregInput = registerVoiceInput({
          insert: (text, live) => {
            Queue.offerUnsafe(queue, Message.VoiceFinalTextReceived({ text, live }));
          },
          insertLive: (text, first) => {
            Queue.offerUnsafe(queue, Message.VoiceLiveTextChanged({ text, first }));
            return true;
          },
        });

        yield* Effect.addFinalizer(() =>
          Effect.sync(() => {
            unsubVoice();
            unregInput();
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
  escapeKey: entry(
    { hasActivePanel: Schema.Boolean },
    {
      modelToDependencies: (model) => ({ hasActivePanel: model.activePanel !== null }),
      dependenciesToStream: ({ hasActivePanel }) =>
        hasActivePanel ? createEscapeKeyStream() : Stream.empty,
    },
  ),
}));
