import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import {
  deleteRecurringTask,
  listRecurringTasks,
  runRecurringTask,
  setRecurringTaskPaused,
} from "@/api/api.gen";
import { answer, request } from "@/lib/effects";
import { showNotice } from "@/lib/notice";
import { Message } from "./message";

const panelOf = (instance: string) => document.getElementById(`${instance}-panel-recurring`);

const Workstream = { owner: Schema.String, name: Schema.String, number: Schema.Number };

export const FetchTasks = Command.define("FetchTasks", {
  args: Workstream,
  messages: [Message.TasksLoaded, Message.LoginRequired, Message.LoadFailed],
  execute: ({ owner, name, number }) =>
    request(() => listRecurringTasks(owner, name, number)).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status !== 200) {
          return Message.LoadFailed({ error: res.data.error });
        }
        return Message.TasksLoaded({ tasks: res.data.data.tasks });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.LoadFailed({ error: error.message })),
      ),
    ),
});

export const SetPaused = Command.define("SetPaused", {
  args: { ...Workstream, id: Schema.Number, paused: Schema.Boolean },
  messages: [Message.PausedChanged, Message.LoginRequired, Message.PauseFailed],
  execute: ({ owner, name, number, id, paused }) => {
    const failed = (error: string) => {
      showNotice(`The recurring task did not ${paused ? "pause" : "resume"}: ${error}`);
      return Message.PauseFailed({ id });
    };
    return request(() => setRecurringTaskPaused(owner, name, number, id, { paused })).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status !== 200) {
          return failed(res.data.error);
        }
        showNotice(`${paused ? "Paused" : "Resumed"} the recurring task`);
        return Message.PausedChanged({ task: res.data.data });
      }),
      Effect.catchTag("RequestFailed", (error) => Effect.succeed(failed(error.message))),
    );
  },
});

export const DeleteTask = Command.define("DeleteTask", {
  args: { ...Workstream, id: Schema.Number },
  messages: [Message.TaskDeleted, Message.LoginRequired, Message.DeleteFailed],
  execute: ({ owner, name, number, id }) =>
    answer(() => deleteRecurringTask(owner, name, number, id), {
      done: () => {
        showNotice("Deleted the recurring task");
        return Message.TaskDeleted({ id });
      },
      login: Message.LoginRequired,
      failed: (error) => {
        showNotice(`The recurring task did not delete: ${error}`);
        return Message.DeleteFailed();
      },
    }),
});

export const OpenTask = Command.define("OpenTask", {
  args: { instance: Schema.String, id: Schema.Number },
  messages: [Message.OpenedTask],
  execute: ({ instance, id }) =>
    Effect.sync(() => Message.OpenedTask({ id, scroll: panelOf(instance)?.scrollTop ?? 0 })),
});

// Sets the scroll position of the panel after the view changes. The view changes in the next animation frame, and the
// frame after it has the new height.
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

export const RunTask = Command.define("RunTask", {
  args: { ...Workstream, id: Schema.Number },
  messages: [Message.RunStarted, Message.LoginRequired, Message.RunRefused],
  execute: ({ owner, name, number, id }) => {
    const refused = (reason: string) => Message.RunRefused({ id, reason });
    return request(() => runRecurringTask(owner, name, number, id)).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status === 409) {
          return refused(res.data.error);
        }
        if (res.status !== 200) {
          return refused(`The recurring task did not run: ${res.data.error}`);
        }
        return Message.RunStarted({ task: res.data.data });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(refused(`The recurring task did not run: ${error.message}`)),
      ),
    );
  },
});

export const ShowNotice = Command.define("ShowNotice", {
  args: { text: Schema.String },
  messages: [Message.CompletedShowNotice],
  execute: ({ text }) =>
    Effect.sync(() => {
      showNotice(text);
      return Message.CompletedShowNotice();
    }),
});
