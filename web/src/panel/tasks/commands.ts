import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import {
  getTask,
  getWorkstreamDetails,
  listTasks,
  resumeIssue,
  startIssue,
  stopIssue,
} from "@/api/api.gen";
import { DraftInboxItem } from "@/chat/schemas";
import { addDraftInboxItem } from "@/lib/draft";
import { answer, request, store } from "@/lib/effects";
import { showNotice } from "@/lib/notice";
import { Message } from "./message";
import { TaskLine } from "./model";

const panelOf = (instance: string) => document.getElementById(`${instance}-panel-tasks`);

export const OpenTask = Command.define("OpenTask", {
  args: { instance: Schema.String, line: TaskLine },
  messages: [Message.OpenedTask],
  execute: ({ instance, line }) =>
    Effect.sync(() => Message.OpenedTask({ line, scroll: panelOf(instance)?.scrollTop ?? 0 })),
});

// The view changes in the next animation frame, and the frame after it has the new height.
export const RestoreScroll = Command.define("RestoreScroll", {
  args: { instance: Schema.String, scroll: Schema.Number },
  messages: [Message.CompletedRestoreScroll],
  execute: ({ instance, scroll }) =>
    Effect.promise(
      () =>
        new Promise<void>((resolve) => {
          requestAnimationFrame(() =>
            requestAnimationFrame(() => {
              const panel = panelOf(instance);
              if (panel) {
                panel.scrollTop = scroll;
              }
              resolve();
            }),
          );
        }),
    ).pipe(Effect.as(Message.CompletedRestoreScroll())),
});

export const FetchTaskDetail = Command.define("FetchTaskDetail", {
  args: {
    owner: Schema.String,
    name: Schema.String,
    number: Schema.Number,
    taskOwner: Schema.String,
    taskName: Schema.String,
    issue: Schema.Number,
  },
  messages: [Message.TaskDetailLoaded, Message.LoginRequired, Message.TaskDetailFailed],
  execute: ({ owner, name, number, taskOwner, taskName, issue }) =>
    request(() => getTask(owner, name, number, taskOwner, taskName, issue)).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status !== 200) {
          return Message.TaskDetailFailed({ error: res.data.error });
        }
        return Message.TaskDetailLoaded({ detail: res.data.data });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.TaskDetailFailed({ error: error.message })),
      ),
    ),
});

export const FetchTasks = Command.define("FetchTasks", {
  args: { owner: Schema.String, name: Schema.String, number: Schema.Number },
  messages: [Message.TasksLoaded, Message.LoginRequired, Message.LoadFailed],
  execute: ({ owner, name, number }) =>
    request(() => listTasks(owner, name, number)).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status !== 200) {
          return Message.LoadFailed({ error: res.data.error });
        }
        return Message.TasksLoaded({ lines: res.data.data });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.LoadFailed({ error: error.message })),
      ),
    ),
});

// The setting of the Workstream is the default of the setting of a task. A failed read keeps the old default.
export const FetchFallback = Command.define("FetchFallback", {
  args: { owner: Schema.String, name: Schema.String, number: Schema.Number },
  messages: [Message.FallbackLoaded, Message.LoginRequired, Message.LoadFailed],
  execute: ({ owner, name, number }) =>
    request(() => getWorkstreamDetails(owner, name, number)).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status !== 200) {
          return Message.LoadFailed({ error: res.data.error });
        }
        return Message.FallbackLoaded({ fallback: res.data.data.implementer });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.LoadFailed({ error: error.message })),
      ),
    ),
});

const Action = Schema.Literals(["start", "start-blocked", "resume", "stop"]);

const run = {
  start: startIssue,
  "start-blocked": startIssue,
  resume: resumeIssue,
  stop: stopIssue,
};

const doneText = {
  start: (reference: string) => `Started ${reference}`,
  "start-blocked": (reference: string) => `${reference} starts when unblocked`,
  resume: (reference: string) => `Resumed ${reference}`,
  stop: (reference: string) => `Stopped ${reference}`,
};

export const ChangeIssue = Command.define("ChangeIssue", {
  args: {
    key: Schema.String,
    action: Action,
    owner: Schema.String,
    name: Schema.String,
    number: Schema.Number,
    reference: Schema.String,
  },
  messages: [Message.ChangedIssue, Message.ChangeFailed],
  execute: ({ key, action, owner, name, number, reference }) =>
    answer(() => run[action](owner, name, number), {
      done: () => {
        showNotice(doneText[action](reference));
        return Message.ChangedIssue({ key });
      },
      failed: (error) => Message.ChangeFailed({ key, error }),
    }),
});

export const AttachTask = Command.define("AttachTask", {
  args: { key: Schema.String, draftKey: Schema.String, item: DraftInboxItem },
  messages: [Message.AttachedTask, Message.AttachFailed],
  execute: ({ key, draftKey, item }) =>
    store(() => addDraftInboxItem(draftKey, item)).pipe(
      Effect.as(Message.AttachedTask()),
      Effect.catchTag("StoreFailed", (error) =>
        Effect.succeed(Message.AttachFailed({ key, error: error.message })),
      ),
    ),
});
