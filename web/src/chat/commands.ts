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
  type ChatInboxItem,
  type InboxItem,
} from "@/api/api.gen";
import {
  answerDraftKey,
  chatDraftKey,
  emptyDraft,
  loadDraft,
  saveDraftText,
  updateDraft,
  type Draft,
} from "@/lib/draft";
import {
  addQueued,
  listQueued,
  newMessageId,
  postQueued,
  removeQueued,
  type Queued,
} from "@/lib/outbox";
import { restoreInboxItem, hideInboxItem } from "@/lib/inbox";
import { issueRef, showNotice } from "@/lib/notice";
import { fitImage, tooManyImages } from "@/lib/images";
import {
  discardVoiceAction,
  finishForSendAction,
  getVoiceSnapshot,
  resendVoiceAction,
  showsLiveVoice,
  stopVoiceAction,
  toggleVoiceAction,
} from "@/lib/voice";
import { Message } from "./message";
import { ports } from "./ports";

// A 401 asks the host for the login.
function withLogin<M>(run: () => Promise<{ message: M; status?: number }>) {
  return Effect.promise(run).pipe(
    Effect.tap(({ status }) =>
      status === 401 ? Port.emit(ports.outbound.loginRequested, undefined) : Effect.void,
    ),
    Effect.map(({ message }) => message),
  );
}

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
    withLogin<ReturnType<typeof Message.PageLoaded> | ReturnType<typeof Message.PageFailed>>(
      async () => {
        try {
          const res = await getChat({ organization, repository, workstream });
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
            const older = await getChat({
              organization,
              repository,
              workstream,
              before: oldest.messages[0].id,
            });
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
        } catch (err) {
          return pageFailed(sent, String(err));
        }
      },
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
    withLogin<ReturnType<typeof Message.OlderPageLoaded> | ReturnType<typeof Message.PageFailed>>(
      async () => {
        try {
          const res = await getChat({ organization, repository, workstream, before });
          if (res.status === 200) {
            return { message: Message.OlderPageLoaded({ page: res.data.data }) };
          }
          return {
            message: Message.PageFailed({
              error:
                res.status === 401
                  ? "login required"
                  : res.data.error || "Failed to load older messages",
            }),
          };
        } catch (err) {
          return { message: Message.PageFailed({ error: String(err) }) };
        }
      },
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
    Effect.promise(async () => {
      try {
        const stored = await listQueued({ organization, repository, workstream });
        return Message.QueueLoaded({ items: stored });
      } catch (err) {
        return Message.QueueFailed({ error: String(err) });
      }
    }),
});

export const EnqueueMessage = Command.define("EnqueueMessage", {
  args: {
    organization: Schema.String,
    repository: Schema.String,
    workstream: Schema.Number,
    text: Schema.String,
    images: Schema.Unknown as Schema.Schema<File[]>,
    inboxItems: Schema.Unknown as Schema.Schema<ChatInboxItem[]>,
  },
  messages: [Message.MessageQueued, Message.MessageRefused],
  execute: ({ organization, repository, workstream, text, images, inboxItems }) =>
    Effect.promise(async () => {
      const item: Queued = {
        id: newMessageId(),
        organization,
        repository,
        workstream,
        text,
        images,
        inboxItems,
        time: new Date().toISOString(),
      };
      try {
        await addQueued(item);
        return Message.MessageQueued({ item });
      } catch (err) {
        return Message.MessageRefused({ error: String(err), text, images, inboxItems });
      }
    }),
});

export const PostQueued = Command.define("PostQueued", {
  args: {
    item: Schema.Unknown as Schema.Schema<Queued>,
    delay: Schema.Number,
  },
  messages: [Message.SendAttemptSucceeded, Message.SendAttemptFailed],
  execute: ({ item, delay }) =>
    withLogin<
      ReturnType<typeof Message.SendAttemptSucceeded> | ReturnType<typeof Message.SendAttemptFailed>
    >(async () => {
      const controller = new AbortController();
      try {
        const res = await postQueued(item, controller.signal);
        if (res.status === 204) {
          await removeQueued(item.id).catch(() => {});
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
      } catch (err) {
        return {
          message: Message.SendAttemptFailed({
            id: item.id,
            error: String(err),
            retryAt: Date.now() + delay,
          }),
        };
      }
    }),
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
    Effect.promise(async () => {
      await removeQueued(id).catch(() => {});
      return Message.CompletedRemoveQueued({ id });
    }),
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
    Effect.promise(async () => {
      await seeChat({ organization, repository, workstream, message }).catch(() => {});
      return Message.CompletedSeeChat();
    }),
});

export const StopChat = Command.define("StopChat", {
  args: {
    organization: Schema.String,
    repository: Schema.String,
    workstream: Schema.Number,
  },
  messages: [Message.StopChatCompleted],
  execute: ({ organization, repository, workstream }) =>
    Effect.promise(async () => {
      try {
        const res = await stopChat({ organization, repository, workstream });
        if (res.status === 204) {
          return Message.StopChatCompleted({ error: null });
        }
        return Message.StopChatCompleted({ error: res.data.error || "Stop failed" });
      } catch (err) {
        return Message.StopChatCompleted({ error: String(err) });
      }
    }),
});

export const LoadDraft = Command.define("LoadDraft", {
  args: {
    organization: Schema.String,
    repository: Schema.String,
    workstream: Schema.Number,
  },
  messages: [Message.DraftLoaded],
  execute: ({ organization, repository, workstream }) =>
    Effect.promise(async () => {
      const key = chatDraftKey(organization, repository, workstream);
      const draft = await loadDraft(key).catch(() => emptyDraft);
      return Message.DraftLoaded({ draft });
    }),
});

export const SaveDraft = Command.define("SaveDraft", {
  args: {
    organization: Schema.String,
    repository: Schema.String,
    workstream: Schema.Number,
    draft: Schema.Unknown as Schema.Schema<Draft>,
  },
  messages: [Message.CompletedSaveDraft],
  execute: ({ organization, repository, workstream, draft }) =>
    Effect.promise(async () => {
      const key = chatDraftKey(organization, repository, workstream);
      await updateDraft(key, () => draft).catch(() => {});
      return Message.CompletedSaveDraft();
    }),
});

export const SaveTrayState = Command.define("SaveTrayState", {
  args: { collapsed: Schema.Boolean },
  messages: [Message.CompletedSaveTrayState],
  execute: ({ collapsed }) =>
    Effect.sync(() => {
      try {
        localStorage.setItem("tray", collapsed ? "collapsed" : "expanded");
      } catch {
        // The tray works with no saved choice.
      }
      return Message.CompletedSaveTrayState();
    }),
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

export const RequestLogin = Command.define("RequestLogin", {
  messages: [Message.CompletedRequestLogin],
  execute: Port.emit(ports.outbound.loginRequested, undefined).pipe(
    Effect.as(Message.CompletedRequestLogin()),
  ),
});

export const AnswerInboxItem = Command.define("AnswerInboxItem", {
  args: {
    id: Schema.Number,
    repository: Schema.String,
    issue: Schema.Number,
    workstream: Schema.Number,
    answer: Schema.String,
  },
  messages: [Message.AnswerCompleted],
  execute: ({ id, repository, issue, workstream, answer }) =>
    withLogin<ReturnType<typeof Message.AnswerCompleted>>(async () => {
      hideInboxItem({ id, repository, issue });
      await updateDraft(answerDraftKey(id), () => emptyDraft).catch(() => {});
      let failure = "";
      let status: number | undefined;
      try {
        const res = await answerInboxItem(id, { text: answer });
        if (res.status === 200) {
          showNotice(
            workstream === 0
              ? "Sent the answer to the Coordinator"
              : `Sent the answer to the Lead of #${workstream}`,
          );
          return { message: Message.AnswerCompleted({ id, result: res.data }) };
        }
        status = res.status;
        failure = res.status === 401 ? "" : res.data.error;
      } catch (err) {
        failure = String(err);
      }
      await saveDraftText(answerDraftKey(id), answer).catch(() => {});
      restoreInboxItem(id);
      return { message: Message.AnswerCompleted({ id, result: { error: failure } }), status };
    }),
});

export const DoneInboxItem = Command.define("DoneInboxItem", {
  args: {
    id: Schema.Number,
    repository: Schema.String,
    issue: Schema.Number,
    name: Schema.String,
    note: Schema.String,
  },
  messages: [Message.AnswerCompleted],
  execute: ({ id, repository, issue, name, note }) =>
    withLogin<ReturnType<typeof Message.AnswerCompleted>>(async () => {
      hideInboxItem({ id, repository, issue });
      await updateDraft(answerDraftKey(id), () => emptyDraft).catch(() => {});
      let failure = "";
      let status: number | undefined;
      try {
        const res = await doneInboxItem(id, { note });
        if (res.status === 200) {
          showNotice(`Marked the todo ${name} as done`);
          return { message: Message.AnswerCompleted({ id, result: res.data }) };
        }
        status = res.status;
        failure = res.status === 401 ? "" : res.data.error;
      } catch (err) {
        failure = String(err);
      }
      await saveDraftText(answerDraftKey(id), note).catch(() => {});
      restoreInboxItem(id);
      return { message: Message.AnswerCompleted({ id, result: { error: failure } }), status };
    }),
});

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
    withLogin<ReturnType<typeof Message.ActionCompleted> | ReturnType<typeof Message.ActionFailed>>(
      async () => {
        if (inboxItemId > 0) {
          hideInboxItem({ id: inboxItemId, repository, issue });
        }
        const failed = (error: string, status?: number) => {
          if (inboxItemId > 0) {
            restoreInboxItem(inboxItemId);
          }
          return { message: Message.ActionFailed({ id: inboxItemId, error }), status };
        };
        try {
          const res = await decisions[decision](actionId, { message: text });
          if (res.status === 200) {
            const decided = res.data.data;
            if (decided.state !== "pending") {
              if (inboxItemId > 0) {
                await updateDraft(answerDraftKey(inboxItemId), () => emptyDraft).catch(() => {});
              }
              showNotice(`${decision === "approve" ? "Approved" : "Rejected"}: ${title}`);
            } else if (decided.error && inboxItemId > 0) {
              restoreInboxItem(inboxItemId);
            }
            return { message: Message.ActionCompleted({ id: inboxItemId, action: decided }) };
          }
          return failed(res.status === 401 ? "" : res.data.error, res.status);
        } catch (err) {
          return failed(String(err));
        }
      },
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
    withLogin<ReturnType<typeof Message.ActionBriefFetched>>(async () => {
      try {
        const res = await getActionBrief(actionId);
        if (res.status === 200) {
          return { message: Message.ActionBriefFetched({ brief: res.data.data.brief }) };
        }
        return { message: Message.ActionBriefFetched({ brief: "" }), status: res.status };
      } catch {
        // The dialog shows the full new Brief.
        return { message: Message.ActionBriefFetched({ brief: "" }) };
      }
    }),
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
    withLogin<ReturnType<typeof Message.ResumeCompleted>>(async () => {
      const [owner, name] = repository.split("/");
      try {
        const res = await resumeIssue(owner, name, issue);
        if (res.status === 204) {
          showNotice(`Resumed ${issueRef(repository, issue, workstreamRepository)}`);
          return { message: Message.ResumeCompleted({ id, error: undefined }) };
        }
        return {
          message: Message.ResumeCompleted({ id, error: res.data.error || "The server failed." }),
          status: res.status,
        };
      } catch (err) {
        return { message: Message.ResumeCompleted({ id, error: String(err) }) };
      }
    }),
});

// The Send again, Dismiss and Merge buttons of a tray row.
export const RunTrayAction = Command.define("RunTrayAction", {
  args: {
    item: Schema.Unknown as Schema.Schema<InboxItem>,
    kind: Schema.Literals(["dismiss", "merge", "send_again"]),
  },
  messages: [Message.TrayActionDone],
  execute: ({ item, kind }) =>
    withLogin<ReturnType<typeof Message.TrayActionDone>>(async () => {
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
      try {
        if (kind === "merge") {
          const res = await mergeInboxItem(id);
          if (res.status === 200) {
            showNotice(
              `Started the merge of PR ${issueRef(item.repository, item.pullRequest, item.workstreamRepository)}`,
            );
            return done("", undefined, res.data.data.merging);
          }
          return done(res.status === 401 ? "" : res.data.error, res.status);
        }
        const res = await (kind === "dismiss" ? dismiss(id) : sendAgain(id));
        if (res.status === 204) {
          if (kind === "dismiss") {
            showNotice(`Dismissed: ${item.title || item.reason}`);
          }
          return done("");
        }
        return done(res.status === 401 ? "" : res.data.error, res.status);
      } catch (err) {
        return done(String(err));
      }
    }),
});

export const ToggleVoice = Command.define("ToggleVoice", {
  messages: [Message.VoicePhaseChanged],
  execute: Effect.sync(() => {
    toggleVoiceAction();
    const snap = getVoiceSnapshot();
    return Message.VoicePhaseChanged({
      phase: snap.phase,
      error: snap.error,
      canResend: snap.canResend,
    });
  }),
});

export const FitImages = Command.define("FitImages", {
  args: { files: Schema.Unknown as Schema.Schema<File[]>, room: Schema.Number },
  messages: [Message.ImagesFitted],
  execute: ({ files, room }) =>
    Effect.promise(async () => {
      let problem = "";
      const added: File[] = [];
      for (const file of files.slice(0, Math.max(0, room))) {
        try {
          added.push(await fitImage(file));
        } catch (err) {
          problem = err instanceof Error ? err.message : String(err);
        }
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

export const ProbeVoice = Command.define("ProbeVoice", {
  args: {
    region: Schema.NullOr(
      Schema.Unknown as Schema.Schema<{ lead: string; trail: string; value: string }>,
    ),
    draftText: Schema.String,
    target: Schema.Literals(["chat", "answer"]),
  },
  messages: [Message.VoiceProbed],
  execute: ({ region, draftText, target }) =>
    Effect.sync(() =>
      Message.VoiceProbed({
        live: showsLiveVoice() && region !== null && region.value === draftText,
        target,
      }),
    ),
});

export const StopVoice = Command.define("StopVoice", {
  messages: [Message.CompletedStopVoice],
  execute: Effect.sync(() => {
    stopVoiceAction();
    return Message.CompletedStopVoice();
  }),
});

export const FinishVoiceForSend = Command.define("FinishVoiceForSend", {
  args: { target: Schema.Literals(["chat", "answer"]) },
  messages: [Message.VoiceSendFinished],
  execute: ({ target }) =>
    Effect.promise(async () => {
      const text = await finishForSendAction();
      return Message.VoiceSendFinished({ text, target });
    }),
});

export const LoadAnswerDraft = Command.define("LoadAnswerDraft", {
  args: { id: Schema.Number },
  messages: [Message.AnswerDraftLoaded],
  execute: ({ id }) =>
    Effect.promise(async () => {
      const draft = await loadDraft(answerDraftKey(id)).catch(() => emptyDraft);
      return Message.AnswerDraftLoaded({ text: draft.text });
    }),
});

export const SaveAnswerDraft = Command.define("SaveAnswerDraft", {
  args: { id: Schema.Number, text: Schema.String },
  messages: [Message.CompletedSaveAnswerDraft],
  execute: ({ id, text }) =>
    Effect.promise(async () => {
      await saveDraftText(answerDraftKey(id), text).catch(() => {});
      return Message.CompletedSaveAnswerDraft();
    }),
});

export const ResendVoice = Command.define("ResendVoice", {
  messages: [Message.VoicePhaseChanged],
  execute: Effect.promise(async () => {
    await resendVoiceAction();
    const snap = getVoiceSnapshot();
    return Message.VoicePhaseChanged({
      phase: snap.phase,
      error: snap.error,
      canResend: snap.canResend,
    });
  }),
});

export const DiscardVoice = Command.define("DiscardVoice", {
  messages: [Message.VoicePhaseChanged],
  execute: Effect.sync(() => {
    discardVoiceAction();
    const snap = getVoiceSnapshot();
    return Message.VoicePhaseChanged({
      phase: snap.phase,
      error: snap.error,
      canResend: snap.canResend,
    });
  }),
});
