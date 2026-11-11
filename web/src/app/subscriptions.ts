import { Effect, Option, Queue, Schema, Stream } from "effect";
import * as Subscription from "foldkit/subscription";
import { subscriptions as agentsSubscriptions } from "@/agents/subscriptions";
import {
  ChatMessage as LiveChatMessage,
  ChatState as LiveChatState,
  InboxItem as LiveInboxItem,
  PendingAction,
} from "@/chat/schemas";
import { inboxItemSubscriptions, subscriptions as chatSubscriptions } from "@/chat/subscriptions";
import { currentNewBuild, reloadOnNewBuild, subscribeNewBuild } from "@/lib/build";
import { currentInbox, openInboxItems, startInbox, subscribeInbox } from "@/lib/inbox";
import { currentNotice, subscribeNotice } from "@/lib/notice";
import { currentUnread, startUnread, subscribeUnread } from "@/lib/unread";
import { currentWorkstreams, startWorkstreams, subscribeWorkstreams } from "@/lib/workstreams";
import { liveEvents, liveListener } from "@/live/events";
import { storeValues } from "@/live/stores";
import { escapeKey } from "@/ui/escape";
import { hasOpenOverlay } from "@/panel/model";
import { subscriptions as panelSubscriptions } from "@/panel/subscriptions";
import { subscriptions as mcpServersSubscriptions } from "@/settings/mcp-servers/subscriptions";
import { subscriptions as memorySubscriptions } from "@/settings/memory/subscriptions";
import { subscriptions as rolesSubscriptions } from "@/settings/roles/subscriptions";
import { subscriptions as tasksSubscriptions } from "@/tasks/subscriptions";
import { Agent } from "@/transcript/schemas";
import { subscriptions as usageSubscriptions } from "@/usage/subscriptions";
import { subscriptions as workstreamsSubscriptions } from "@/workstreams/subscriptions";
import { Message } from "./message";
import { CIStop, Drain, Pause, Switches, Upgrade, wideQuery, type Model } from "./model";

const AnyRecord = Schema.Record(Schema.String, Schema.Json);
const RecurringTaskEvent = Schema.Struct({
  repository: Schema.String,
  number: Schema.Number,
});

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

const wideStream = Stream.callback<Message>((queue) => {
  const query = window.matchMedia(wideQuery);
  const onChange = () => Queue.offerUnsafe(queue, Message.WideChanged({ wide: query.matches }));
  return Effect.gen(function* () {
    query.addEventListener("change", onChange);
    yield* Effect.addFinalizer(() =>
      Effect.sync(() => query.removeEventListener("change", onChange)),
    );
  });
});

const upgradeClock = Stream.callback<Message>((queue) => {
  const tick = () => Queue.offerUnsafe(queue, Message.UpgradeTicked({ now: Date.now() }));
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

// The old server answers until it restarts, so the page reloads when the new build comes.
const reloadStream = Stream.callback<Message>(() => {
  const controller = new AbortController();
  void reloadOnNewBuild(controller.signal);
  return Effect.addFinalizer(() => Effect.sync(() => controller.abort()));
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
                liveListener("upgrade", Upgrade, (upgrade) => Message.UpgradeChanged({ upgrade })),
                liveListener("workstreams", AnyRecord, () => Message.ForwardWorkstreamsEvent()),
                liveListener("agent", Agent, (agent) => Message.ForwardAgentEvent({ agent })),
                liveListener("recurring_tasks", RecurringTaskEvent, ({ repository, number }) =>
                  Message.ForwardRecurringEvent({ repository, number }),
                ),
                liveListener("inbox", LiveInboxItem, () => Message.ForwardInboxEvent()),
                liveListener("message", LiveChatMessage, (message) =>
                  Message.ForwardLiveMessage({ message }),
                ),
                liveListener("chat", LiveChatState, (state) =>
                  Message.ForwardLiveChatState({ state }),
                ),
                liveListener("action", PendingAction, (action) =>
                  Message.ForwardLiveAction({ action }),
                ),
                liveListener("question", LiveInboxItem, (question) =>
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

  upgradeClock: entry(
    { waiting: Schema.Boolean },
    {
      modelToDependencies: (model) => ({ waiting: model.upgrade.step === "retry" }),
      dependenciesToStream: ({ waiting }) => (waiting ? upgradeClock : Stream.empty),
    },
  ),

  reloadOnNewBuild: entry(
    { restarting: Schema.Boolean },
    {
      modelToDependencies: (model) => ({ restarting: model.upgrade.step === "restart" }),
      dependenciesToStream: ({ restarting }) => (restarting ? reloadStream : Stream.empty),
    },
  ),

  online: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () => onlineStream,
    },
  ),

  escape: entry(
    { open: Schema.Boolean },
    {
      modelToDependencies: (model) => ({
        open:
          Option.isSome(model.openMenu) ||
          model.upgrade.changesShown ||
          Option.isSome(model.heldSheet) ||
          model.loginShown ||
          (model.mobileTasksOpen && !Option.exists(model.panel, hasOpenOverlay)),
      }),
      dependenciesToStream: ({ open }) =>
        open ? escapeKey(Message.PressedEscape()) : Stream.empty,
    },
  ),

  wide: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () => wideStream,
    },
  ),

  login: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () => loginStream,
    },
  ),
}));

const liftedChat = Subscription.lift(chatSubscriptions)<Model, Message>({
  read: (model) => model.chat,
  toParentMessage: (message) => Message.GotChatMessage({ message }),
  when: (model) => model.route._tag === "CoordinatorChat" || model.route._tag === "WorkstreamChat",
});

const liftedInbox = Subscription.lift(inboxItemSubscriptions)<Model, Message>({
  read: (model) => model.inboxProgram,
  toParentMessage: (message) => Message.GotInboxMessage({ message }),
  when: (model) => model.route._tag === "Inbox",
});

const liftedPanel = Subscription.lift(panelSubscriptions)<Model, Message>({
  read: (model) => model.panel,
  toParentMessage: (message) => Message.GotPanelMessage({ message }),
  when: (model) => model.route._tag === "WorkstreamChat",
});

const liftedTasks = Subscription.lift(tasksSubscriptions)<Model, Message>({
  read: (model) => model.tasksProgram,
  toParentMessage: (message) => Message.GotTasksMessage({ message }),
  when: (model) => model.route._tag === "Tasks",
});

const liftedAgents = Subscription.lift(agentsSubscriptions)<Model, Message>({
  read: (model) => model.agentsProgram,
  toParentMessage: (message) => Message.GotAgentsMessage({ message }),
  when: (model) => model.route._tag === "Agents",
});

const liftedWorkstreams = Subscription.lift(workstreamsSubscriptions)<Model, Message>({
  read: (model) => model.workstreamsProgram,
  toParentMessage: (message) => Message.GotWorkstreamsMessage({ message }),
  when: (model) => model.route._tag === "Workstreams",
});

const liftedUsage = Subscription.lift(usageSubscriptions)<Model, Message>({
  read: (model) => model.usageProgram,
  toParentMessage: (message) => Message.GotUsageMessage({ message }),
  when: (model) => model.route._tag === "Usage",
});

const liftedMemory = Subscription.lift(memorySubscriptions)<Model, Message>({
  read: (model) => model.memoryProgram,
  toParentMessage: (message) => Message.GotMemoryMessage({ message }),
  when: (model) =>
    model.route._tag === "MemoryRepositories" ||
    model.route._tag === "Memory" ||
    model.route._tag === "MemoryFile",
});

const liftedRoles = Subscription.lift(rolesSubscriptions)<Model, Message>({
  read: (model) => model.rolesProgram,
  toParentMessage: (message) => Message.GotRolesMessage({ message }),
  when: (model) => model.route._tag === "Roles",
});

const liftedMcpServers = Subscription.lift(mcpServersSubscriptions)<Model, Message>({
  read: (model) => model.mcpServersProgram,
  toParentMessage: (message) => Message.GotMcpServersMessage({ message }),
  when: (model) => model.route._tag === "McpServers",
});

export const subscriptions = Subscription.aggregate(
  appSubscriptions,
  liftedChat,
  liftedInbox,
  liftedPanel,
  liftedTasks,
  liftedAgents,
  liftedWorkstreams,
  liftedUsage,
  liftedMemory,
  liftedRoles,
  liftedMcpServers,
);
