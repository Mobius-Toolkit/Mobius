import { Effect, Queue, Schema, Stream } from "effect";
import * as Port from "foldkit/port";
import * as Subscription from "foldkit/subscription";
import { listGitHubApps } from "@/api/api.gen";
import { retryDelay } from "@/lib/events";
import { sameChat } from "@/lib/unread";
import { getVoiceSnapshot, registerVoiceInput, subscribeVoice } from "@/lib/voice";
import { answerInputId, inputId } from "./composer";
import { request } from "./errors";
import { Message } from "./message";
import type { Model } from "./model";
import { ports } from "./ports";
import { ChatMessage, ChatState, InboxItem, PendingAction } from "./schemas";

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

// The newest input gets the voice text: the field of the answer panel, when it is open, else the field of the chat.
function textArea(id: string) {
  const element = document.getElementById(id);
  return element instanceof HTMLTextAreaElement ? element : null;
}

function voiceInput() {
  return textArea(answerInputId) ?? textArea(inputId);
}

type VoiceRegion = { lead: string; trail: string; value: string };

function createVoiceStream() {
  return Stream.callback<Message>(
    (queue) =>
      Effect.gen(function* () {
        // The text around the voice text of the run, for each field. An edit of the Owner in a field ends its region,
        // and a render that sets the Model value again must not end it.
        const regions = new Map<string, VoiceRegion>();
        const edited = (event: Event) => {
          if (event.target instanceof Element) {
            regions.delete(event.target.id);
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
          regions.set(target.id, { lead, trail, value: target.value });
          Queue.offerUnsafe(
            queue,
            Message.VoiceTextPlaced({
              value,
              lead,
              trail,
              target: target.id === answerInputId ? "answer" : "chat",
            }),
          );
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
            const target = voiceInput();
            if (!target) {
              return;
            }
            const region = regions.get(target.id);
            if (live && region && target.value === region.value) {
              place(target, voiceText, region.lead, region.trail);
            } else if (voiceText) {
              placeAtCursor(target, voiceText);
            }
          },
          insertLive: (voiceText, first) => {
            const target = voiceInput();
            if (!target) {
              return false;
            }
            if (first) {
              // A region belongs to one run.
              regions.clear();
              placeAtCursor(target, voiceText);
              return true;
            }
            const region = regions.get(target.id);
            if (!region || target.value !== region.value) {
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
          // A new connection ends a retry that waits.
          clearTimeout(retryTimer);
          if (source) {
            source.close();
          }
          connect();
        };

        const reconnectUnlessStopped = Effect.sync(() => {
          if (!stopped) {
            reconnect();
          }
        });

        const handleRetry = () => {
          Effect.runFork(
            request(() => listGitHubApps()).pipe(
              Effect.flatMap((res) =>
                res.status === 401
                  ? Effect.sync(() =>
                      Queue.offerUnsafe(queue, Message.PageFailed({ error: "login required" })),
                    )
                  : reconnectUnlessStopped,
              ),
              Effect.catchTag("RequestFailed", () => reconnectUnlessStopped),
            ),
          );
        };

        const listen = <T>(
          name: string,
          schema: Schema.Decoder<T>,
          toMessage: (value: T) => Message | null,
        ) => {
          const decode = Schema.decodeUnknownEffect(Schema.fromJsonString(schema));
          source?.addEventListener(name, (event) => {
            if (!(event instanceof MessageEvent)) {
              return;
            }
            Effect.runFork(
              decode(event.data).pipe(
                Effect.flatMap((value) =>
                  Effect.sync(() => {
                    const message = toMessage(value);
                    if (message !== null) {
                      Queue.offerUnsafe(queue, message);
                    }
                  }),
                ),
                Effect.catchTag("SchemaError", (error) =>
                  Effect.logWarning(`The live event ${name} has no valid data`, error.message),
                ),
              ),
            );
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

          source.addEventListener("ping", resetSilenceWatchdog);

          listen("message", ChatMessage, (message) =>
            sameChat(message, target) ? Message.ReceivedLiveMessage({ message }) : null,
          );
          listen("chat", ChatState, (state) =>
            sameChat(state, target) ? Message.ReceivedLiveChatState({ state }) : null,
          );
          listen("action", PendingAction, (action) => Message.ReceivedLiveAction({ action }));
          listen("question", InboxItem, (question) => Message.ReceivedLiveQuestion({ question }));
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
        window.addEventListener("pageshow", handleRestore);
        document.addEventListener("visibilitychange", handleWake);

        connect();

        yield* Effect.addFinalizer(() =>
          Effect.sync(() => {
            stopped = true;
            clearTimeout(silenceTimer);
            clearTimeout(retryTimer);
            window.removeEventListener("online", handleWake);
            window.removeEventListener("pageshow", handleRestore);
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
  escapeKey: entry(
    { hasActivePanel: Schema.Boolean },
    {
      modelToDependencies: (model) => ({ hasActivePanel: model.activePanel !== null }),
      dependenciesToStream: ({ hasActivePanel }) =>
        hasActivePanel ? createEscapeKeyStream() : Stream.empty,
    },
  ),
}));
