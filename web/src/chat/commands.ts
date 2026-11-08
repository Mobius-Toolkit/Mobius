import { Effect, Schema } from "effect";
import * as Command from "foldkit/command";
import * as Port from "foldkit/port";
import {
  answerInboxItem,
  approveAction,
  dismiss,
  doneInboxItem,
  getActionBrief,
  getChat,
  mergeInboxItem,
  rejectAction,
  resumeIssue,
  seeChat,
  sendAgain,
  stopChat,
} from "@/api/api.gen";
import {
  addDraftInboxItem,
  answerDraftKey,
  chatDraftKey,
  type Draft as StoredDraft,
  type DraftInboxItem,
  type DraftVoice,
  updateDraft,
} from "@/lib/draft";
import { StoreFailed, fitted, request, store } from "@/lib/effects";
import { fitImage, tooManyImages } from "@/lib/images";
import { restoreInboxItem, hideInboxItem } from "@/lib/inbox";
import { issueRef, showNotice } from "@/lib/notice";
import { addQueued, listQueued, newMessageId, postQueued, removeQueued } from "@/lib/outbox";
import { resend, stopVoice, toggleVoice } from "@/lib/voice";
import { Message } from "./message";
import { draftInboxItem } from "./model";
import { organizationRequested, ports } from "./ports";
import { DraftInboxItems, Images, InboxItem, PendingAction, Queued } from "./schemas";

type Outcome = { message: Message; status?: number | undefined };

// A 401 asks the host for the login.
function withLogin<A extends Outcome>(
  outcome: Effect.Effect<A>,
): Effect.Effect<NoInfer<A>["message"]> {
  return outcome.pipe(
    Effect.tap(({ status }) =>
      status === 401 ? Port.emit(ports.outbound.loginRequested, undefined) : Effect.void,
    ),
    Effect.map(({ message }) => message),
  );
}

const clearDraft = (key: string) =>
  store(() =>
    updateDraft(key, (draft) => ({ ...draft, text: "", images: [], inboxItems: [], voice: null })),
  ).pipe(Effect.ignore);

const readyVoice: DraftVoice = { state: "ready", audio: null, conversion: "", error: "" };

// Puts a draft that did not go out back in the store. The Owner can have typed or added an item in the meantime.
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

const showNoticeOf = (text: string) => Effect.sync(() => showNotice(text));

function pageFailed(sent: string | null, error: string) {
  return { message: Message.PageFailed({ error, sent: sent ?? undefined }) };
}

// A load after a reconnect gets pages until one has the newest message that the list got before. sent is the id of
// the message whose send started this load. The message leaves the queue when the load ends.
export const FetchPage = Command.define("FetchPage", {
  args: {
    organization: Schema.String,
    repository: Schema.String,
    workstream: Schema.Number,
    isInitial: Schema.Boolean,
    known: Schema.NullOr(Schema.Number),
    sent: Schema.NullOr(Schema.String),
  },
  messages: [Message.PageLoaded, Message.PageFailed],
  execute: ({ organization, repository, workstream, isInitial, known, sent }) =>
    withLogin(
      Effect.gen(function* () {
        const res = yield* request(() => getChat({ organization, repository, workstream }));
        if (res.status === 401) {
          return pageFailed(sent, "login required");
        }
        if (res.status !== 200) {
          return pageFailed(sent, res.data.error || "Failed to load chat");
        }
        const page = res.data.data;
        const messages = [...page.messages];
        let oldest = page;
        for (;;) {
          if (known === null || !oldest.older || oldest.messages[0].id <= known) {
            break;
          }
          const older = yield* request(() =>
            getChat({ organization, repository, workstream, before: oldest.messages[0].id }),
          );
          if (older.status === 401) {
            return pageFailed(sent, "login required");
          }
          if (older.status !== 200) {
            return pageFailed(sent, older.data.error || "Failed to load chat");
          }
          oldest = older.data.data;
          messages.unshift(...oldest.messages);
        }
        return {
          message: Message.PageLoaded({
            page: { ...page, messages },
            isInitial,
            sent: sent ?? undefined,
          }),
        };
      }).pipe(
        Effect.catchTag("RequestFailed", (error) =>
          Effect.succeed(pageFailed(sent, error.message)),
        ),
      ),
    ),
});

export const FetchOlder = Command.define("FetchOlder", {
  args: {
    organization: Schema.String,
    repository: Schema.String,
    workstream: Schema.Number,
    before: Schema.Number,
  },
  messages: [Message.OlderPageLoaded, Message.PageFailed],
  execute: ({ organization, repository, workstream, before }) =>
    withLogin(
      request(() => getChat({ organization, repository, workstream, before })).pipe(
        Effect.map((res) =>
          res.status === 200
            ? { message: Message.OlderPageLoaded({ page: res.data.data }) }
            : {
                message: Message.PageFailed({
                  error:
                    res.status === 401
                      ? "login required"
                      : res.data.error || "Failed to load older messages",
                }),
              },
        ),
        Effect.catchTag("RequestFailed", (error) =>
          Effect.succeed({ message: Message.PageFailed({ error: error.message }) }),
        ),
      ),
    ),
});

export const ListQueued = Command.define("ListQueued", {
  args: {
    organization: Schema.String,
    repository: Schema.String,
    workstream: Schema.Number,
  },
  messages: [Message.QueueLoaded, Message.QueueFailed],
  execute: ({ organization, repository, workstream }) =>
    store(() => listQueued({ organization, repository, workstream })).pipe(
      Effect.flatMap(Schema.decodeUnknownEffect(Schema.Array(Queued))),
      Effect.map((items) => Message.QueueLoaded({ items })),
      Effect.catch((error) => Effect.succeed(Message.QueueFailed({ error: error.message }))),
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

export const PostQueued = Command.define("PostQueued", {
  args: {
    item: Queued,
    delay: Schema.Number,
  },
  messages: [Message.SendAttemptSucceeded, Message.SendAttemptFailed],
  execute: ({ item, delay }) =>
    withLogin(
      Effect.gen(function* () {
        const res = yield* request((signal) => postQueued(item, signal));
        if (res.status === 204) {
          yield* store(() => removeQueued(item.id)).pipe(Effect.ignore);
          return { message: Message.SendAttemptSucceeded({ id: item.id }) };
        }
        return {
          message: Message.SendAttemptFailed({
            id: item.id,
            error: res.error || "Send failed",
            retryAt: Date.now() + delay,
          }),
          status: res.status,
        };
      }).pipe(
        Effect.catchTag("RequestFailed", (error) =>
          Effect.succeed({
            message: Message.SendAttemptFailed({
              id: item.id,
              error: error.message,
              retryAt: Date.now() + delay,
            }),
          }),
        ),
      ),
    ),
});

export const WaitToRetry = Command.define("WaitToRetry", {
  args: { id: Schema.String, delay: Schema.Number, sequence: Schema.Number },
  messages: [Message.RetryDue],
  execute: ({ id, delay, sequence }) =>
    Effect.sleep(delay).pipe(Effect.as(Message.RetryDue({ id, sequence }))),
});

export const RemoveQueued = Command.define("RemoveQueued", {
  args: {
    id: Schema.String,
  },
  messages: [Message.CompletedRemoveQueued],
  execute: ({ id }) =>
    store(() => removeQueued(id)).pipe(
      Effect.ignore,
      Effect.as(Message.CompletedRemoveQueued({ id })),
    ),
});

export const SeeChat = Command.define("SeeChat", {
  args: {
    organization: Schema.String,
    repository: Schema.String,
    workstream: Schema.Number,
    message: Schema.Number,
  },
  messages: [Message.CompletedSeeChat],
  execute: ({ organization, repository, workstream, message }) =>
    request(() => seeChat({ organization, repository, workstream, message })).pipe(
      Effect.ignore,
      Effect.as(Message.CompletedSeeChat()),
    ),
});

export const StopChat = Command.define("StopChat", {
  args: {
    organization: Schema.String,
    repository: Schema.String,
    workstream: Schema.Number,
  },
  messages: [Message.StopChatCompleted],
  execute: ({ organization, repository, workstream }) =>
    request(() => stopChat({ organization, repository, workstream })).pipe(
      Effect.map((res) =>
        Message.StopChatCompleted({
          error: res.status === 204 ? null : res.data.error || "Stop failed",
        }),
      ),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.StopChatCompleted({ error: error.message })),
      ),
    ),
});

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

export const SaveTrayState = Command.define("SaveTrayState", {
  args: { collapsed: Schema.Boolean },
  messages: [Message.CompletedSaveTrayState],
  execute: ({ collapsed }) =>
    Effect.try({
      try: () => localStorage.setItem("tray", collapsed ? "collapsed" : "expanded"),
      catch: (cause) => new StoreFailed({ message: String(cause) }),
    }).pipe(
      // The tray works with no saved choice.
      Effect.ignore,
      Effect.as(Message.CompletedSaveTrayState()),
    ),
});

export const Navigate = Command.define("Navigate", {
  args: {
    path: Schema.String,
  },
  messages: [Message.CompletedNavigate],
  execute: ({ path }) =>
    Port.emit(ports.outbound.navigationRequested, path).pipe(
      Effect.as(Message.CompletedNavigate()),
    ),
});

// The Chat button of an Inbox row puts the item in the draft of the chat of its Workstream, or of the Coordinator chat of
// its organization, and opens that chat.
export const OpenChatWithItem = Command.define("OpenChatWithItem", {
  args: { item: InboxItem, organization: Schema.String },
  messages: [Message.CompletedOpenChat],
  execute: ({ item, organization }) =>
    Effect.gen(function* () {
      const inWorkstream = item.workstream !== 0;
      const key = inWorkstream
        ? chatDraftKey(
            item.workstreamRepository.split("/")[0],
            item.workstreamRepository,
            item.workstream,
          )
        : chatDraftKey(item.organization, "", 0);
      yield* store(() => addDraftInboxItem(key, draftInboxItem(item)));
      if (inWorkstream) {
        yield* Port.emit(
          ports.outbound.navigationRequested,
          `/workstreams/${item.workstreamRepository}/${item.workstream}`,
        );
        return;
      }
      if (item.organization !== organization) {
        yield* Port.emit(organizationRequested, item.organization);
      }
      yield* Port.emit(ports.outbound.navigationRequested, "/chat");
    }).pipe(
      // A draft that the store refused leaves the Owner in the Inbox.
      Effect.ignore,
      Effect.as(Message.CompletedOpenChat()),
    ),
});

export const RequestLogin = Command.define("RequestLogin", {
  messages: [Message.CompletedRequestLogin],
  execute: Port.emit(ports.outbound.loginRequested, undefined).pipe(
    Effect.as(Message.CompletedRequestLogin()),
  ),
});

type Refusal = { error: string; status?: number };

// A 401 has no text: the host shows the login.
const refusalOf = (res: { status: number; data: { error: string } }): Refusal => ({
  error: res.status === 401 ? "" : res.data.error,
  status: res.status,
});

const unreachable = (error: { message: string }): Effect.Effect<Refusal> =>
  Effect.succeed({ error: error.message });

export const AnswerInboxItem = Command.define("AnswerInboxItem", {
  args: {
    id: Schema.Number,
    repository: Schema.String,
    issue: Schema.Number,
    workstream: Schema.Number,
    answer: Schema.String,
    voice: Schema.Boolean,
  },
  messages: [Message.AnswerCompleted],
  execute: ({ id, repository, issue, workstream, answer, voice }) =>
    withLogin(
      Effect.gen(function* () {
        yield* Effect.sync(() => hideInboxItem({ id, repository, issue }));
        yield* clearDraft(answerDraftKey(id));
        const refusal = yield* request(() => answerInboxItem(id, { text: answer })).pipe(
          Effect.map((res) => (res.status === 200 ? null : refusalOf(res))),
          Effect.catchTag("RequestFailed", unreachable),
        );
        if (refusal === null) {
          yield* showNoticeOf(
            workstream === 0
              ? "Sent the answer to the Coordinator"
              : `Sent the answer to the Lead of #${workstream}`,
          );
          return { message: Message.AnswerCompleted({ id, error: "" }) };
        }
        yield* restoreDraft(answerDraftKey(id), {
          text: answer,
          images: [],
          inboxItems: [],
          voice,
        });
        yield* Effect.sync(() => restoreInboxItem(id));
        return {
          message: Message.AnswerCompleted({ id, error: refusal.error }),
          status: refusal.status,
        };
      }),
    ),
});

export const DoneInboxItem = Command.define("DoneInboxItem", {
  args: {
    id: Schema.Number,
    repository: Schema.String,
    issue: Schema.Number,
    name: Schema.String,
    note: Schema.String,
    voice: Schema.Boolean,
  },
  messages: [Message.AnswerCompleted],
  execute: ({ id, repository, issue, name, note, voice }) =>
    withLogin(
      Effect.gen(function* () {
        yield* Effect.sync(() => hideInboxItem({ id, repository, issue }));
        yield* clearDraft(answerDraftKey(id));
        const refusal = yield* request(() => doneInboxItem(id, { note })).pipe(
          Effect.map((res) => (res.status === 200 ? null : refusalOf(res))),
          Effect.catchTag("RequestFailed", unreachable),
        );
        if (refusal === null) {
          yield* showNoticeOf(`Marked the todo ${name} as done`);
          return { message: Message.AnswerCompleted({ id, error: "" }) };
        }
        yield* restoreDraft(answerDraftKey(id), { text: note, images: [], inboxItems: [], voice });
        yield* Effect.sync(() => restoreInboxItem(id));
        return {
          message: Message.AnswerCompleted({ id, error: refusal.error }),
          status: refusal.status,
        };
      }),
    ),
});

type Decided = {
  message: ReturnType<typeof Message.ActionCompleted> | ReturnType<typeof Message.ActionFailed>;
  status?: number | undefined;
};

const decisions = { approve: approveAction, reject: rejectAction };

// A decision hides the Inbox item while the request runs. A failure brings the item back with the error. The answer of
// the server with the action that still waits has the error in the action.
function decide(decision: "approve" | "reject") {
  return ({
    actionId,
    inboxItemId,
    repository,
    issue,
    title,
    text,
  }: {
    actionId: number;
    inboxItemId: number;
    repository: string;
    issue: number;
    title: string;
    text: string;
  }) =>
    withLogin(
      Effect.gen(function* () {
        if (inboxItemId > 0) {
          yield* Effect.sync(() => hideInboxItem({ id: inboxItemId, repository, issue }));
        }
        const failed = (error: string, status?: number): Effect.Effect<Decided> =>
          Effect.sync(() => {
            if (inboxItemId > 0) {
              restoreInboxItem(inboxItemId);
            }
          }).pipe(Effect.as({ message: Message.ActionFailed({ id: inboxItemId, error }), status }));
        const completed = (decided: PendingAction): Effect.Effect<Decided> =>
          Effect.gen(function* () {
            if (decided.state !== "pending") {
              if (inboxItemId > 0) {
                yield* clearDraft(answerDraftKey(inboxItemId));
              }
              yield* showNoticeOf(`${decision === "approve" ? "Approved" : "Rejected"}: ${title}`);
            } else if (decided.error && inboxItemId > 0) {
              yield* Effect.sync(() => restoreInboxItem(inboxItemId));
            }
            return { message: Message.ActionCompleted({ id: inboxItemId, action: decided }) };
          });
        return yield* request(() => decisions[decision](actionId, { message: text })).pipe(
          Effect.flatMap((res) =>
            res.status === 200
              ? completed(res.data.data)
              : failed(res.status === 401 ? "" : res.data.error, res.status),
          ),
          Effect.catchTag("RequestFailed", (error) => failed(error.message)),
        );
      }),
    );
}

const decisionArgs = {
  actionId: Schema.Number,
  inboxItemId: Schema.Number,
  repository: Schema.String,
  issue: Schema.Number,
  title: Schema.String,
  text: Schema.String,
};

export const ApproveAction = Command.define("ApproveAction", {
  args: decisionArgs,
  messages: [Message.ActionCompleted, Message.ActionFailed],
  execute: decide("approve"),
});

export const RejectAction = Command.define("RejectAction", {
  args: decisionArgs,
  messages: [Message.ActionCompleted, Message.ActionFailed],
  execute: decide("reject"),
});

export const FetchActionBrief = Command.define("FetchActionBrief", {
  args: {
    actionId: Schema.Number,
  },
  messages: [Message.ActionBriefFetched],
  execute: ({ actionId }) =>
    withLogin(
      request(() => getActionBrief(actionId)).pipe(
        Effect.map((res) =>
          res.status === 200
            ? { message: Message.ActionBriefFetched({ brief: res.data.data.brief }) }
            : { message: Message.ActionBriefFetched({ brief: "" }), status: res.status },
        ),
        // The dialog shows the full new Brief.
        Effect.catchTag("RequestFailed", () =>
          Effect.succeed({ message: Message.ActionBriefFetched({ brief: "" }) }),
        ),
      ),
    ),
});

export const ResumeIssue = Command.define("ResumeIssue", {
  args: {
    id: Schema.Number,
    repository: Schema.String,
    issue: Schema.Number,
    workstreamRepository: Schema.String,
  },
  messages: [Message.ResumeCompleted],
  execute: ({ id, repository, issue, workstreamRepository }) =>
    withLogin(
      Effect.gen(function* () {
        const [owner, name] = repository.split("/");
        const res = yield* request(() => resumeIssue(owner, name, issue));
        if (res.status === 204) {
          yield* showNoticeOf(`Resumed ${issueRef(repository, issue, workstreamRepository)}`);
          return { message: Message.ResumeCompleted({ id, error: undefined }) };
        }
        return {
          message: Message.ResumeCompleted({ id, error: res.data.error || "The server failed." }),
          status: res.status,
        };
      }).pipe(
        Effect.catchTag("RequestFailed", (error) =>
          Effect.succeed({ message: Message.ResumeCompleted({ id, error: error.message }) }),
        ),
      ),
    ),
});

// The Send again, Dismiss and Merge buttons of a tray row.
export const RunTrayAction = Command.define("RunTrayAction", {
  args: {
    item: InboxItem,
    kind: Schema.Literals(["dismiss", "merge", "send_again"]),
  },
  messages: [Message.TrayActionDone],
  execute: ({ item, kind }) =>
    withLogin(
      Effect.gen(function* () {
        const id = item.id;
        const done = (error: string, status?: number, merging = false) => ({
          message: Message.TrayActionDone({
            id,
            kind: kind === "merge" ? "merge" : kind === "dismiss" ? "dismiss" : "action",
            error,
            merging,
          }),
          status,
        });
        if (kind === "merge") {
          const res = yield* request(() => mergeInboxItem(id));
          if (res.status === 200) {
            yield* showNoticeOf(
              `Started the merge of PR ${issueRef(item.repository, item.pullRequest, item.workstreamRepository)}`,
            );
            return done("", undefined, res.data.data.merging);
          }
          return done(res.status === 401 ? "" : res.data.error, res.status);
        }
        const res = yield* request(() => (kind === "dismiss" ? dismiss(id) : sendAgain(id)));
        if (res.status === 204) {
          if (kind === "dismiss") {
            yield* showNoticeOf(`Dismissed: ${item.title || item.reason}`);
          }
          return done("");
        }
        return done(res.status === 401 ? "" : res.data.error, res.status);
      }).pipe(
        Effect.catchTag("RequestFailed", (error) =>
          Effect.succeed({
            message: Message.TrayActionDone({
              id: item.id,
              kind: kind === "merge" ? "merge" : kind === "dismiss" ? "dismiss" : "action",
              error: error.message,
              merging: false,
            }),
          }),
        ),
      ),
    ),
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
  messages: [Message.CompletedClickAttach],
  execute: Effect.sync(() => {
    document.getElementById("chat-attach")?.click();
    return Message.CompletedClickAttach();
  }),
});

export const ToggleVoice = Command.define("ToggleVoice", {
  args: { key: Schema.String },
  messages: [Message.CompletedToggleVoice],
  execute: ({ key }) =>
    Effect.sync(() => {
      toggleVoice(key);
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
  args: { key: Schema.String },
  messages: [Message.CompletedResendVoice],
  execute: ({ key }) =>
    store(() => resend(key)).pipe(Effect.ignore, Effect.as(Message.CompletedResendVoice())),
});

export const RemoveVoice = Command.define("RemoveVoice", {
  args: { key: Schema.String },
  messages: [Message.CompletedRemoveVoice],
  execute: ({ key }) =>
    savedDraft(key, (draft) => ({ ...draft, voice: null })).pipe(
      Effect.as(Message.CompletedRemoveVoice()),
    ),
});

const markDuration = 2000;
const markTimers = new WeakMap<Element, ReturnType<typeof setTimeout>>();

// Scrolls to the row of a blocking todo and marks it for a short time.
export const ShowBlockingTodo = Command.define("ShowBlockingTodo", {
  args: { id: Schema.Number },
  messages: [Message.CompletedShowBlockingTodo],
  execute: ({ id }) =>
    Effect.sync(() => {
      const row = document.querySelector(`[data-inbox-item="${id}"]`);
      if (row) {
        row.scrollIntoView({ block: "center" });
        row.setAttribute("data-marked", "");
        clearTimeout(markTimers.get(row));
        markTimers.set(
          row,
          setTimeout(() => row.removeAttribute("data-marked"), markDuration),
        );
      }
      return Message.CompletedShowBlockingTodo();
    }),
});
