import { Effect, Option, Queue, Schema, Stream } from "effect";
import * as Subscription from "foldkit/subscription";
import { toGotTranscriptMessage } from "@/agents/message";
import { answerInputId, inputId } from "@/chat/composer";
import { Message as ChatMsg } from "@/chat/message";
import { ChatMessage, ChatState, InboxItem, PendingAction } from "@/chat/schemas";
import { draftValues, voiceClock, voiceInput } from "@/chat/subscriptions";
import { currentNewBuild, subscribeNewBuild } from "@/lib/build";
import { answerDraftKey, chatDraftKey } from "@/lib/draft";
import { currentInbox, openInboxItems, startInbox, subscribeInbox } from "@/lib/inbox";
import { currentNotice, subscribeNotice } from "@/lib/notice";
import { currentUnread, startUnread, subscribeUnread, unreadCount } from "@/lib/unread";
import { subscribeVoice, voiceSnapshot } from "@/lib/voice";
import { currentWorkstreams, startWorkstreams, subscribeWorkstreams } from "@/lib/workstreams";
import { liveEvents, liveListener } from "@/live/events";
import { storeValues } from "@/live/stores";
import { Message as PanelMsg } from "@/panel/message";
import { toGotTranscriptMessage as toGotAgentsTranscriptMessage } from "@/panel/agents/message";
import { tasksClock } from "@/tasks/subscriptions";
import { transcriptStream } from "@/transcript/stream";
import { Message } from "./message";
import { CIStop, Drain, Pause, Switches, type Model } from "./model";

const AnyRecord = Schema.Record(Schema.String, Schema.Json);
const RecurringTaskEvent = Schema.Struct({
  repository: Schema.String,
  number: Schema.Number,
});
const UpgradeEvent = Schema.Struct({ failure: Schema.String });

const onlineStream = Stream.callback<Message>((queue) => {
  const onOnline = () => Queue.offerUnsafe(queue, Message.OnlineChanged({ online: true }));
  const onOffline = () => Queue.offerUnsafe(queue, Message.OnlineChanged({ online: false }));
  return Effect.gen(function* () {
    window.addEventListener("online", onOnline);
    window.addEventListener("offline", onOffline);
    yield* Effect.addFinalizer(() =>
      Effect.sync(() => {
        window.removeEventListener("online", onOnline);
        window.removeEventListener("offline", onOffline);
      }),
    );
  });
});

const loginStream = Stream.callback<Message>((queue) => {
  const onLogin = () => Queue.offerUnsafe(queue, Message.ShowLogin());
  return Effect.gen(function* () {
    window.addEventListener("mobius:login", onLogin);
    yield* Effect.addFinalizer(() =>
      Effect.sync(() => {
        window.removeEventListener("mobius:login", onLogin);
      }),
    );
  });
});

const switchStream = Stream.callback<Message>((queue) => {
  const onSwitch = (event: Event) => {
    const custom = event as CustomEvent<{
      kind: "start" | "merge" | "repairs";
      on: boolean;
    }>;
    if (custom.detail) {
      Queue.offerUnsafe(
        queue,
        Message.ToggleSwitchRequested({
          kind: custom.detail.kind,
          on: custom.detail.on,
        }),
      );
    }
  };
  return Effect.gen(function* () {
    window.addEventListener("mobius:switch", onSwitch);
    yield* Effect.addFinalizer(() =>
      Effect.sync(() => {
        window.removeEventListener("mobius:switch", onSwitch);
      }),
    );
  });
});

function createEscapeKeyStream() {
  return Stream.callback<Message>((queue) => {
    const listener = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        Queue.offerUnsafe(queue, Message.GotChatMessage({ message: ChatMsg.ClosePanel() }));
        Queue.offerUnsafe(queue, Message.GotInboxMessage({ message: ChatMsg.ClosePanel() }));
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

const stopDelay = 500;

function OptionIsLive(model: Model): boolean {
  if (Option.isNone(model.apps)) {
    return false;
  }
  return model.apps.value.length > 0;
}

export const appSubscriptions = Subscription.make<Model, Message>()((entry) => ({
  liveEvents: entry(
    { live: Schema.Boolean },
    {
      modelToDependencies: (model) => ({
        live: !model.loginShown && OptionIsLive(model),
      }),
      dependenciesToStream: ({ live }) =>
        live
          ? liveEvents<Message>({
              opened: Message.LiveOpened(),
              connecting: Message.LiveConnecting(),
              connected: Message.LiveConnected(),
              loginRequired: Message.LiveLoginRequired(),
              onSource: (source) => {
                const stopUnread = startUnread(source);
                const stopWorkstreams = startWorkstreams(source, () =>
                  window.dispatchEvent(new CustomEvent("mobius:login")),
                );
                const stopInbox = startInbox(source);
                return () => {
                  stopUnread();
                  stopWorkstreams();
                  stopInbox();
                };
              },
              listeners: [
                liveListener("repositories", AnyRecord, () => Message.RepositoriesChanged()),
                liveListener("ciStops", Schema.Array(CIStop), (stops) =>
                  Message.CIStopsChanged({ stops }),
                ),
                liveListener("pauses", Schema.Array(Pause), (pauses) =>
                  Message.PausesChanged({ pauses }),
                ),
                liveListener("switches", Switches, (switches) =>
                  Message.SwitchesChanged({ switches }),
                ),
                liveListener("drain", Drain, (drain) => Message.DrainChanged({ drain })),
                liveListener("upgrade", UpgradeEvent, (u) =>
                  Message.UpgradeFailureChanged({ failure: u.failure }),
                ),
                liveListener("workstreams", AnyRecord, () => Message.ForwardWorkstreamsEvent()),
                liveListener("agent", AnyRecord, (agent) => Message.ForwardAgentEvent({ agent })),
                liveListener("recurring_tasks", RecurringTaskEvent, ({ repository, number }) =>
                  Message.ForwardRecurringEvent({ repository, number }),
                ),
                liveListener("inbox", InboxItem, () => Message.ForwardInboxEvent()),
                liveListener("message", ChatMessage, (message) =>
                  Message.ForwardLiveMessage({ message }),
                ),
                liveListener("chat", ChatState, (state) => Message.ForwardLiveChatState({ state })),
                liveListener("action", PendingAction, (action) =>
                  Message.ForwardLiveAction({ action }),
                ),
                liveListener("question", InboxItem, (question) =>
                  Message.ForwardLiveQuestion({ question }),
                ),
              ],
            })
          : Stream.empty,
    },
  ),

  unread: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () =>
        storeValues(subscribeUnread, currentUnread, (unread) =>
          Message.UnreadStoreChanged({ unread: unread ?? [] }),
        ),
    },
  ),

  workstreams: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () =>
        storeValues(subscribeWorkstreams, currentWorkstreams, (state) =>
          Message.WorkstreamsStoreChanged({
            workstreams: state.list ?? [],
            needsHuman: state.needsHuman,
            working: Array.from(state.working),
          }),
        ),
    },
  ),

  inbox: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () =>
        storeValues(subscribeInbox, currentInbox, (inbox) =>
          Message.InboxStoreChanged({
            items: openInboxItems(inbox),
            answered: inbox.answered,
          }),
        ),
    },
  ),

  notice: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () =>
        storeValues(subscribeNotice, currentNotice, (notice) =>
          Message.NoticeStoreChanged({ notice }),
        ),
    },
  ),

  newBuild: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () =>
        storeValues(subscribeNewBuild, currentNewBuild, (newBuild) =>
          Message.NewBuildStoreChanged({ newBuild }),
        ),
    },
  ),

  online: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () => onlineStream,
    },
  ),

  login: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () => loginStream,
    },
  ),

  switchRequest: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () => switchStream,
    },
  ),

  chatUnread: entry(
    { organization: Schema.String, repository: Schema.String, workstream: Schema.Number },
    {
      modelToDependencies: (model) => {
        if (Option.isSome(model.chat)) {
          const { organization, repository, workstream } = model.chat.value;
          return { organization, repository, workstream };
        }
        return { organization: "", repository: "", workstream: 0 };
      },
      dependenciesToStream: (chat) =>
        chat.organization === ""
          ? Stream.empty
          : Stream.map(
              storeValues(subscribeUnread, currentUnread, (list) =>
                ChatMsg.ReceivedUnread({
                  unread: list === undefined ? null : unreadCount(list, chat),
                }),
              ),
              (msg) => Message.GotChatMessage({ message: msg }),
            ),
    },
  ),

  chatBrief: entry(
    { repository: Schema.String, number: Schema.Number },
    {
      modelToDependencies: (model) => {
        if (Option.isSome(model.chat) && model.route._tag === "WorkstreamChat") {
          return {
            repository: `${model.route.owner}/${model.route.name}`,
            number: model.route.number,
          };
        }
        return { repository: "", number: 0 };
      },
      dependenciesToStream: ({ repository, number }) =>
        repository === ""
          ? Stream.empty
          : Stream.map(
              storeValues(subscribeWorkstreams, currentWorkstreams, (state) =>
                ChatMsg.ReceivedBrief({
                  brief:
                    state.list?.find((w) => w.repository === repository && w.number === number) ??
                    null,
                }),
              ),
              (msg) => Message.GotChatMessage({ message: msg }),
            ),
    },
  ),

  panelBrief: entry(
    { repository: Schema.String, number: Schema.Number },
    {
      modelToDependencies: (model) => {
        if (Option.isSome(model.panel) && model.route._tag === "WorkstreamChat") {
          return {
            repository: `${model.route.owner}/${model.route.name}`,
            number: model.route.number,
          };
        }
        return { repository: "", number: 0 };
      },
      dependenciesToStream: ({ repository, number }) =>
        repository === ""
          ? Stream.empty
          : Stream.map(
              storeValues(subscribeWorkstreams, currentWorkstreams, (state) =>
                PanelMsg.ReceivedBrief({
                  brief:
                    state.list?.find((w) => w.repository === repository && w.number === number)
                      ?.brief ?? "",
                }),
              ),
              (msg) => Message.GotPanelMessage({ message: msg }),
            ),
    },
  ),

  chatTrayItems: entry(
    { repository: Schema.String, workstream: Schema.Number, organization: Schema.String },
    {
      modelToDependencies: (model) => {
        if (Option.isSome(model.chat)) {
          return {
            repository: model.chat.value.repository,
            workstream: model.chat.value.workstream,
            organization: model.chat.value.organization,
          };
        }
        return { repository: "", workstream: 0, organization: "" };
      },
      dependenciesToStream: ({ repository, workstream, organization }) =>
        organization === ""
          ? Stream.empty
          : Stream.map(
              storeValues(subscribeInbox, currentInbox, (inbox) => {
                const items = openInboxItems(inbox);
                const trayItems = repository
                  ? items.filter(
                      (it) =>
                        it.workstreamRepository === repository && it.workstream === workstream,
                    )
                  : items.filter((it) => it.organization === organization);
                return ChatMsg.ReceivedTrayItems({ items: trayItems });
              }),
              (msg) => Message.GotChatMessage({ message: msg }),
            ),
    },
  ),

  inboxTrayItems: entry(
    { organization: Schema.String },
    {
      modelToDependencies: (model) => ({
        organization: Option.isSome(model.inboxProgram) ? model.organization : "",
      }),
      dependenciesToStream: ({ organization }) =>
        organization === ""
          ? Stream.empty
          : Stream.map(
              storeValues(subscribeInbox, currentInbox, (inbox) => {
                const items = openInboxItems(inbox);
                const trayItems = items.filter((it) => it.organization === organization);
                return ChatMsg.ReceivedTrayItems({ items: trayItems });
              }),
              (msg) => Message.GotInboxMessage({ message: msg }),
            ),
    },
  ),

  chatDraft: entry(
    { key: Schema.String },
    {
      modelToDependencies: (model) => {
        if (Option.isSome(model.chat)) {
          const { organization, repository, workstream } = model.chat.value;
          return { key: chatDraftKey(organization, repository, workstream) };
        }
        return { key: "" };
      },
      dependenciesToStream: ({ key }) =>
        key === ""
          ? Stream.empty
          : Stream.map(
              draftValues(key, (draft) => ChatMsg.ReceivedDraft({ draft })),
              (msg) => Message.GotChatMessage({ message: msg }),
            ),
    },
  ),

  chatVoiceInput: entry(
    { key: Schema.String },
    {
      modelToDependencies: (model) => {
        if (Option.isSome(model.chat)) {
          const { organization, repository, workstream } = model.chat.value;
          return { key: chatDraftKey(organization, repository, workstream) };
        }
        return { key: "" };
      },
      dependenciesToStream: ({ key }) =>
        key === ""
          ? Stream.empty
          : Stream.map(voiceInput(key, inputId), (msg) => Message.GotChatMessage({ message: msg })),
    },
  ),

  chatStopReady: entry(
    { showStop: Schema.Boolean },
    {
      modelToDependencies: (model) => {
        if (Option.isSome(model.chat)) {
          const c = model.chat.value;
          return {
            showStop:
              c.stoppable &&
              !c.draftText.trim() &&
              c.draftImages.length === 0 &&
              c.draftInboxItems.length === 0,
          };
        }
        return { showStop: false };
      },
      dependenciesToStream: ({ showStop }) =>
        Stream.concat(
          Stream.make(
            Message.GotChatMessage({
              message: ChatMsg.StopReadyChanged({ ready: false }),
            }),
          ),
          showStop
            ? Stream.fromEffect(
                Effect.sleep(stopDelay).pipe(
                  Effect.as(
                    Message.GotChatMessage({
                      message: ChatMsg.StopReadyChanged({ ready: true }),
                    }),
                  ),
                ),
              )
            : Stream.empty,
        ),
    },
  ),

  chatAnswerDraft: entry(
    { id: Schema.NullOr(Schema.Number) },
    {
      modelToDependencies: (model) => {
        if (Option.isSome(model.chat)) {
          const panel = model.chat.value.activePanel;
          return { id: panel?.type === "answer" ? panel.item.id : null };
        }
        return { id: null };
      },
      dependenciesToStream: ({ id }) =>
        id === null
          ? Stream.empty
          : Stream.map(
              draftValues(answerDraftKey(id), (draft) =>
                ChatMsg.ReceivedAnswerDraft({ id, draft }),
              ),
              (msg) => Message.GotChatMessage({ message: msg }),
            ),
    },
  ),

  chatAnswerVoiceInput: entry(
    { id: Schema.NullOr(Schema.Number) },
    {
      modelToDependencies: (model) => {
        if (Option.isSome(model.chat)) {
          const panel = model.chat.value.activePanel;
          return { id: panel?.type === "answer" ? panel.item.id : null };
        }
        return { id: null };
      },
      dependenciesToStream: ({ id }) =>
        id === null
          ? Stream.empty
          : Stream.map(voiceInput(answerDraftKey(id), answerInputId), (msg) =>
              Message.GotChatMessage({ message: msg }),
            ),
    },
  ),

  inboxAnswerDraft: entry(
    { id: Schema.NullOr(Schema.Number) },
    {
      modelToDependencies: (model) => {
        if (Option.isSome(model.inboxProgram)) {
          const panel = model.inboxProgram.value.activePanel;
          return { id: panel?.type === "answer" ? panel.item.id : null };
        }
        return { id: null };
      },
      dependenciesToStream: ({ id }) =>
        id === null
          ? Stream.empty
          : Stream.map(
              draftValues(answerDraftKey(id), (draft) =>
                ChatMsg.ReceivedAnswerDraft({ id, draft }),
              ),
              (msg) => Message.GotInboxMessage({ message: msg }),
            ),
    },
  ),

  inboxAnswerVoiceInput: entry(
    { id: Schema.NullOr(Schema.Number) },
    {
      modelToDependencies: (model) => {
        if (Option.isSome(model.inboxProgram)) {
          const panel = model.inboxProgram.value.activePanel;
          return { id: panel?.type === "answer" ? panel.item.id : null };
        }
        return { id: null };
      },
      dependenciesToStream: ({ id }) =>
        id === null
          ? Stream.empty
          : Stream.map(voiceInput(answerDraftKey(id), answerInputId), (msg) =>
              Message.GotInboxMessage({ message: msg }),
            ),
    },
  ),

  voiceStore: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () =>
        storeValues(subscribeVoice, voiceSnapshot, (voice) =>
          Message.GotChatMessage({
            message: ChatMsg.VoiceChanged({ voice }),
          }),
        ),
    },
  ),

  voiceClock: entry(
    { recording: Schema.Boolean },
    {
      modelToDependencies: (model) => ({
        recording:
          (Option.isSome(model.chat) && model.chat.value.voice.recording) ||
          (Option.isSome(model.inboxProgram) && model.inboxProgram.value.voice.recording),
      }),
      dependenciesToStream: ({ recording }) =>
        recording
          ? Stream.map(voiceClock, (msg) => Message.GotChatMessage({ message: msg }))
          : Stream.empty,
    },
  ),

  escapeKey: entry(
    { hasActivePanel: Schema.Boolean },
    {
      modelToDependencies: (model) => ({
        hasActivePanel:
          (Option.isSome(model.chat) && model.chat.value.activePanel !== null) ||
          (Option.isSome(model.inboxProgram) && model.inboxProgram.value.activePanel !== null),
      }),
      dependenciesToStream: ({ hasActivePanel }) =>
        hasActivePanel ? createEscapeKeyStream() : Stream.empty,
    },
  ),

  tasksClock: entry(
    { active: Schema.Boolean },
    {
      modelToDependencies: (model) => ({
        active:
          model.route._tag === "Tasks" ||
          (Option.isSome(model.panel) && model.panel.value.tab === "tasks"),
      }),
      dependenciesToStream: ({ active }) =>
        active
          ? Stream.map(tasksClock, (msg) => Message.GotTasksMessage({ message: msg }))
          : Stream.empty,
    },
  ),

  agentsTranscript: entry(
    { id: Schema.NullOr(Schema.Number) },
    {
      modelToDependencies: (model) => {
        if (
          Option.isSome(model.agentsProgram) &&
          Option.isSome(model.agentsProgram.value.transcript)
        ) {
          return { id: model.agentsProgram.value.transcript.value.agent.id };
        }
        return { id: null };
      },
      dependenciesToStream: ({ id }) =>
        id === null
          ? Stream.empty
          : Stream.map(transcriptStream(id), (msg) =>
              Message.GotAgentsMessage({
                message: toGotTranscriptMessage(msg),
              }),
            ),
    },
  ),

  panelTranscript: entry(
    { id: Schema.NullOr(Schema.Number) },
    {
      modelToDependencies: (model) => {
        if (
          Option.isSome(model.panel) &&
          model.panel.value.tab === "agents" &&
          Option.isSome(model.panel.value.agents.transcript)
        ) {
          return { id: model.panel.value.agents.transcript.value.agent.id };
        }
        return { id: null };
      },
      dependenciesToStream: ({ id }) =>
        id === null
          ? Stream.empty
          : Stream.map(transcriptStream(id), (msg) =>
              Message.GotPanelMessage({
                message: PanelMsg.GotAgentsMessage({
                  message: toGotAgentsTranscriptMessage(msg),
                }),
              }),
            ),
    },
  ),
}));

export const subscriptions = appSubscriptions;
