import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import { getWorkstreamDetails, listTasks, resumeIssue, startIssue, stopIssue } from "@/api/api.gen";
import { answer, request } from "@/lib/effects";
import { showNotice } from "@/lib/notice";
import { Message } from "./message";

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

const Action = Schema.Literals(["start", "resume", "stop"]);

const run = {
  start: startIssue,
  resume: resumeIssue,
  stop: stopIssue,
};

const doneText = {
  start: "Started",
  resume: "Resumed",
  stop: "Stopped",
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
        showNotice(`${doneText[action]} ${reference}`);
        return Message.ChangedIssue({ key });
      },
      failed: (error) => Message.ChangeFailed({ key, error }),
    }),
});
