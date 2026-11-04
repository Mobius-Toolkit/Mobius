import { Effect, Schema } from "effect";
import * as Command from "foldkit/command";
import * as Port from "foldkit/port";
import {
  answerInboxItem,
  approveAction,
  getActionBrief,
  getChat,
  rejectAction,
  resumeIssue,
  seeChat,
  stopChat,
  type ChatInboxItem,
} from "@/api/api.gen";
import { chatDraftKey, emptyDraft, loadDraft, updateDraft, type Draft } from "@/lib/draft";
import {
  addQueued,
  listQueued,
  newMessageId,
  postQueued,
  removeQueued,
  type Queued,
} from "@/lib/outbox";
import { failInboxItem, hideInboxItem } from "@/lib/inbox";
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

// A load after a reconnect gets pages until one has the newest message that the list got before.
export const FetchPage = Command.define("FetchPage", {
  args: {
    organization: Schema.String,
    repository: Schema.String,
    workstream: Schema.Number,
    isInitial: Schema.Boolean,
    known: Schema.NullOr(Schema.Number),
  },
  messages: [Message.PageLoaded, Message.PageFailed],
  execute: ({ organization, repository, workstream, isInitial, known }) =>
    Effect.promise(async () => {
      try {
        const res = await getChat({ organization, repository, workstream });
        if (res.status === 401) {
          return Message.PageFailed({ error: "login required" });
        }
        if (res.status !== 200) {
          return Message.PageFailed({ error: res.data.error || "Failed to load chat" });
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
            return Message.PageFailed({ error: "login required" });
          }
          if (older.status !== 200) {
            return Message.PageFailed({ error: older.data.error || "Failed to load chat" });
          }
          oldest = older.data.data;
          messages.unshift(...oldest.messages);
        }
        return Message.PageLoaded({ page: { ...page, messages }, isInitial });
      } catch (err) {
        return Message.PageFailed({ error: String(err) });
      }
    }),
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
    Effect.promise(async () => {
      try {
        const res = await getChat({ organization, repository, workstream, before });
        if (res.status === 200) {
          return Message.OlderPageLoaded({ page: res.data.data });
        }
        return Message.PageFailed({ error: res.data.error || "Failed to load older messages" });
      } catch (err) {
        return Message.PageFailed({ error: String(err) });
      }
    }),
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
    Effect.promise(async () => {
      const controller = new AbortController();
      try {
        const res = await postQueued(item, controller.signal);
        if (res.status === 204) {
          await removeQueued(item.id).catch(() => {});
          return Message.SendAttemptSucceeded({ id: item.id });
        }
        return Message.SendAttemptFailed({
          id: item.id,
          error: res.error || "Send failed",
          retryAt: Date.now() + delay,
        });
      } catch (err) {
        return Message.SendAttemptFailed({
          id: item.id,
          error: String(err),
          retryAt: Date.now() + delay,
        });
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
    Effect.promise(async () => {
      hideInboxItem({ id, repository, issue });
      try {
        const res = await answerInboxItem(id, { text: answer });
        if (res.status === 200) {
          showNotice(
            workstream === 0
              ? "Sent the answer to the Coordinator"
              : `Sent the answer to the Lead of #${workstream}`,
          );
          return Message.AnswerCompleted({ result: res.data });
        }
        failInboxItem(id, res.data.error);
        return Message.AnswerCompleted({ result: { error: res.data.error } });
      } catch (err) {
        failInboxItem(id, String(err));
        return Message.AnswerCompleted({ result: { error: String(err) } });
      }
    }),
});

export const ApproveAction = Command.define("ApproveAction", {
  args: {
    actionId: Schema.Number,
    inboxItemId: Schema.Number,
    repository: Schema.String,
    issue: Schema.Number,
    title: Schema.String,
    text: Schema.String,
  },
  messages: [Message.ActionCompleted],
  execute: ({ actionId, inboxItemId, repository, issue, title, text }) =>
    Effect.promise(async () => {
      if (inboxItemId > 0) {
        hideInboxItem({ id: inboxItemId, repository, issue });
      }
      try {
        const res = await approveAction(actionId, { message: text });
        if (res.status === 200) {
          const decided = res.data.data;
          if (decided.state !== "pending") {
            showNotice(`Approved: ${title}`);
          } else if (decided.error && inboxItemId > 0) {
            failInboxItem(inboxItemId, decided.error);
          }
          return Message.ActionCompleted({ action: decided });
        }
        if (inboxItemId > 0) {
          failInboxItem(inboxItemId, res.data.error);
        }
        return Message.ActionCompleted({ action: { error: res.data.error } });
      } catch (err) {
        if (inboxItemId > 0) {
          failInboxItem(inboxItemId, String(err));
        }
        return Message.ActionCompleted({ action: { error: String(err) } });
      }
    }),
});

export const RejectAction = Command.define("RejectAction", {
  args: {
    actionId: Schema.Number,
    inboxItemId: Schema.Number,
    repository: Schema.String,
    issue: Schema.Number,
    title: Schema.String,
    text: Schema.String,
  },
  messages: [Message.ActionCompleted],
  execute: ({ actionId, inboxItemId, repository, issue, title, text }) =>
    Effect.promise(async () => {
      if (inboxItemId > 0) {
        hideInboxItem({ id: inboxItemId, repository, issue });
      }
      try {
        const res = await rejectAction(actionId, { message: text });
        if (res.status === 200) {
          const decided = res.data.data;
          if (decided.state !== "pending") {
            showNotice(`Rejected: ${title}`);
          } else if (decided.error && inboxItemId > 0) {
            failInboxItem(inboxItemId, decided.error);
          }
          return Message.ActionCompleted({ action: decided });
        }
        if (inboxItemId > 0) {
          failInboxItem(inboxItemId, res.data.error);
        }
        return Message.ActionCompleted({ action: { error: res.data.error } });
      } catch (err) {
        if (inboxItemId > 0) {
          failInboxItem(inboxItemId, String(err));
        }
        return Message.ActionCompleted({ action: { error: String(err) } });
      }
    }),
});

export const FetchActionBrief = Command.define("FetchActionBrief", {
  args: {
    actionId: Schema.Number,
  },
  messages: [Message.ActionBriefFetched],
  execute: ({ actionId }) =>
    Effect.promise(async () => {
      try {
        const res = await getActionBrief(actionId);
        if (res.status === 200) {
          return Message.ActionBriefFetched({ brief: res.data.data.brief });
        }
      } catch {
        // ignore
      }
      return Message.ActionBriefFetched({ brief: "" });
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
    Effect.promise(async () => {
      const [owner, name] = repository.split("/");
      try {
        const res = await resumeIssue(owner, name, issue);
        if (res.status === 204) {
          showNotice(`Resumed ${issueRef(repository, issue, workstreamRepository)}`);
          return Message.ResumeCompleted({ id, error: undefined });
        }
        return Message.ResumeCompleted({ id, error: res.data.error || "The server failed." });
      } catch (err) {
        return Message.ResumeCompleted({ id, error: String(err) });
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
  },
  messages: [Message.VoiceProbed],
  execute: ({ region, draftText }) =>
    Effect.sync(() =>
      Message.VoiceProbed({
        live: showsLiveVoice() && region !== null && region.value === draftText,
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
  messages: [Message.VoiceSendFinished],
  execute: Effect.promise(async () => {
    const text = await finishForSendAction();
    return Message.VoiceSendFinished({ text });
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
