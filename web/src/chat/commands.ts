import { Effect, Schema } from "effect";
import * as Command from "foldkit/command";
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
  type DraftInboxItem,
  type DraftSelection,
} from "@/lib/draft";
import { clearDraft, restoreDraft, sentInboxItems, sentSelections } from "@/composer/commands";
import { StoreFailed, request, store } from "@/lib/effects";
import { restoreInboxItem, hideInboxItem } from "@/lib/inbox";
import { pushUrl } from "@/lib/navigation";
import { issueRef, showNotice } from "@/lib/notice";
import { listQueued, postQueued, removeQueued } from "@/lib/outbox";
import { Message } from "./message";
import { draftInboxItem } from "./model";
import {
  DraftInboxItems,
  DraftSelections,
  Images,
  InboxItem,
  PendingAction,
  Queued,
  StoredQueued,
} from "./schemas";

type Outcome = { message: Message; status?: number | undefined };

// A 401 asks the host for the login.
function withLogin<A extends Outcome>(
  outcome: Effect.Effect<A>,
): Effect.Effect<NoInfer<A>["message"]> {
  return outcome.pipe(
    Effect.tap(({ status }) =>
      status === 401
        ? Effect.sync(() => window.dispatchEvent(new CustomEvent("mobius:login")))
        : Effect.void,
    ),
    Effect.map(({ message }) => message),
  );
}

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

export const readQueued = (record: typeof StoredQueued.Type): Queued => ({
  ...record,
  inboxItems: record.inboxItems ?? [],
  selections: record.selections ?? [],
  voice: record.voice ?? false,
  voiceSeconds: record.voiceSeconds ?? 0,
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
      Effect.flatMap(Schema.decodeUnknownEffect(Schema.Array(StoredQueued))),
      Effect.map((records) =>
        Message.QueueLoaded({
          items: records.map(readQueued).toSorted((a, b) => a.time.localeCompare(b.time)),
        }),
      ),
      Effect.catch((error) => Effect.succeed(Message.QueueFailed({ error: error.message }))),
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
  execute: ({ path }) => pushUrl(path).pipe(Effect.as(Message.CompletedNavigate())),
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
        const path = `/workstreams/${item.workstreamRepository}/${item.workstream}`;
        yield* pushUrl(path);
        return;
      }
      if (item.organization !== organization) {
        try {
          localStorage.setItem("organization", item.organization);
        } catch {}
      }
      const chatPath = "/chat";
      yield* pushUrl(chatPath);
    }).pipe(
      // A draft that the store refused leaves the Owner in the Inbox.
      Effect.ignore,
      Effect.as(Message.CompletedOpenChat()),
    ),
});

export const RequestLogin = Command.define("RequestLogin", {
  messages: [Message.CompletedRequestLogin],
  execute: Effect.sync(() => {
    window.dispatchEvent(new CustomEvent("mobius:login"));
  }).pipe(Effect.as(Message.CompletedRequestLogin())),
});

type Refusal = { error: string; status?: number };

// A 401 has no text: the host shows the login.
const refusalOf = (res: { status: number; data: { error: string } }): Refusal => ({
  error: res.status === 401 ? "" : res.data.error,
  status: res.status,
});

const unreachable = (error: { message: string }): Effect.Effect<Refusal> =>
  Effect.succeed({ error: error.message });

// The parts of the draft that go with the text of a request.
const partArgs = {
  images: Images,
  inboxItems: DraftInboxItems,
  selections: DraftSelections,
  voice: Schema.Boolean,
  voiceSeconds: Schema.Number,
};

type Parts = {
  images: File[];
  inboxItems: DraftInboxItem[];
  selections: DraftSelection[];
  voice: boolean;
  voiceSeconds: number;
};

const bodyParts = ({ images, inboxItems, selections, voice, voiceSeconds }: Parts) => ({
  images,
  inboxItems: sentInboxItems(inboxItems),
  selections: sentSelections(selections),
  voice,
  voiceSeconds,
});

const restoreParts = (key: string, text: string, parts: Parts) =>
  restoreDraft(key, { text, ...parts });

export const AnswerInboxItem = Command.define("AnswerInboxItem", {
  args: {
    id: Schema.Number,
    repository: Schema.String,
    issue: Schema.Number,
    answer: Schema.String,
    notice: Schema.String,
    ...partArgs,
  },
  messages: [Message.AnswerCompleted],
  execute: ({ id, repository, issue, answer, notice, ...parts }) =>
    withLogin(
      Effect.gen(function* () {
        yield* Effect.sync(() => hideInboxItem({ id, repository, issue }));
        yield* clearDraft(answerDraftKey(id));
        const refusal = yield* request(() =>
          answerInboxItem(id, { text: answer, ...bodyParts(parts) }),
        ).pipe(
          Effect.map((res) => (res.status === 200 ? null : refusalOf(res))),
          Effect.catchTag("RequestFailed", unreachable),
        );
        if (refusal === null) {
          yield* showNoticeOf(notice);
          return { message: Message.AnswerCompleted({ id, error: "" }) };
        }
        yield* restoreParts(answerDraftKey(id), answer, parts);
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
    notice: Schema.String,
    note: Schema.String,
    ...partArgs,
  },
  messages: [Message.AnswerCompleted],
  execute: ({ id, repository, issue, notice, note, ...parts }) =>
    withLogin(
      Effect.gen(function* () {
        yield* Effect.sync(() => hideInboxItem({ id, repository, issue }));
        yield* clearDraft(answerDraftKey(id));
        const refusal = yield* request(() => doneInboxItem(id, { note, ...bodyParts(parts) })).pipe(
          Effect.map((res) => (res.status === 200 ? null : refusalOf(res))),
          Effect.catchTag("RequestFailed", unreachable),
        );
        if (refusal === null) {
          yield* showNoticeOf(notice);
          return { message: Message.AnswerCompleted({ id, error: "" }) };
        }
        yield* restoreParts(answerDraftKey(id), note, parts);
        yield* Effect.sync(() => restoreInboxItem(id));
        return {
          message: Message.AnswerCompleted({ id, error: refusal.error }),
          status: refusal.status,
        };
      }),
    ),
});

// A Resume with no note has no comment to send, so it calls the resume of the issue.
export const ResumeStoppedItem = Command.define("ResumeStoppedItem", {
  args: {
    id: Schema.Number,
    repository: Schema.String,
    issue: Schema.Number,
    notice: Schema.String,
  },
  messages: [Message.AnswerCompleted],
  execute: ({ id, repository, issue, notice }) =>
    withLogin(
      Effect.gen(function* () {
        const [owner, name] = repository.split("/");
        yield* Effect.sync(() => hideInboxItem({ id, repository, issue }));
        yield* clearDraft(answerDraftKey(id));
        const refusal = yield* request(() => resumeIssue(owner, name, issue)).pipe(
          Effect.map((res) => (res.status === 204 ? null : refusalOf(res))),
          Effect.catchTag("RequestFailed", unreachable),
        );
        if (refusal === null) {
          yield* showNoticeOf(notice);
          return { message: Message.AnswerCompleted({ id, error: "" }) };
        }
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
    ...parts
  }: Parts & {
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
        return yield* request(() =>
          decisions[decision](actionId, { message: text, ...bodyParts(parts) }),
        ).pipe(
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
  ...partArgs,
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
            if (item.todo !== null) {
              yield* Effect.sync(() =>
                hideInboxItem({ id, repository: item.repository, issue: item.issue }),
              );
            }
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
