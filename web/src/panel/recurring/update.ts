import { Option } from "effect";
import * as Update from "foldkit/update";
import {
  DeleteTask,
  FetchTasks,
  OpenTask,
  RestoreScroll,
  RunTask,
  SetPaused,
  ShowNotice,
} from "./commands";
import { Message, OutMessage } from "./message";
import type { Model } from "./model";

type UpdateReturn = Update.ReturnWithOutMessage<Model, Message, OutMessage>;

const workstream = (model: Model) => ({
  owner: model.owner,
  name: model.name,
  number: model.number,
});

export const reload = (model: Model) => [FetchTasks(workstream(model))];

export const refresh = (model: Model): Update.Return<Model, Message> => ({
  model,
  commands: reload(model),
});

const idle = (model: Model, id: number): Model => ({
  ...model,
  busy: model.busy.filter((busyId) => busyId !== id),
});

const leave = (model: Model): UpdateReturn => ({
  model: { ...model, opened: Option.none() },
  commands: [RestoreScroll({ instance: model.instance, scroll: model.scroll })],
});

const stopped = (model: Model, id: number): Model => ({
  ...model,
  running: model.running.filter((runningId) => runningId !== id),
});

export function update(model: Model, message: Message): UpdateReturn {
  return Message.match<UpdateReturn>(message, {
    TasksLoaded: ({ tasks }) => {
      const loaded = { ...model, tasks: Option.some(tasks), error: "" };
      return Option.exists(model.opened, (id) => !tasks.some((task) => task.id === id))
        ? leave(loaded)
        : { model: loaded };
    },
    LoadFailed: ({ error }) => ({ model: { ...model, error } }),
    LoginRequired: () => ({
      model: {
        ...model,
        busy: [],
        running: [],
        deletion: Option.map(model.deletion, (deletion) => ({ ...deletion, busy: false })),
      },
      outMessage: OutMessage.LoginRequired(),
    }),
    ClickedRow: ({ id }) => ({
      model,
      commands: [OpenTask({ instance: model.instance, id })],
    }),
    OpenedTask: ({ id, scroll }) => ({ model: { ...model, opened: Option.some(id), scroll } }),
    ClickedBack: () => leave(model),
    CompletedRestoreScroll: () => ({ model }),
    ClickedRun: ({ id }) => ({
      model: { ...model, running: [...model.running.filter((runningId) => runningId !== id), id] },
      commands: [RunTask({ ...workstream(model), id })],
    }),
    RunStarted: ({ task }) => ({
      model: {
        ...stopped(model, task.id),
        tasks: Option.map(model.tasks, (tasks) =>
          tasks.map((other) => (other.id === task.id ? task : other)),
        ),
      },
      commands: [ShowNotice({ text: "The Lead got the event." })],
    }),
    RunRefused: ({ id, reason }) => ({
      model: stopped(model, id),
      commands: [ShowNotice({ text: reason })],
    }),
    CompletedShowNotice: () => ({ model }),
    ClickedPause: ({ id, paused }) => ({
      model: { ...model, busy: [...model.busy.filter((busyId) => busyId !== id), id] },
      commands: [SetPaused({ ...workstream(model), id, paused })],
    }),
    PausedChanged: ({ task }) => ({
      model: {
        ...idle(model, task.id),
        tasks: Option.map(model.tasks, (tasks) =>
          tasks.map((other) => (other.id === task.id ? task : other)),
        ),
      },
    }),
    PauseFailed: ({ id }) => ({ model: idle(model, id) }),
    ClickedDelete: ({ id, text }) => ({
      model: { ...model, deletion: Option.some({ id, text, busy: false }) },
    }),
    ClickedConfirmDelete: () => {
      if (Option.isNone(model.deletion)) {
        return { model };
      }
      const { id, text } = model.deletion.value;
      return {
        model: { ...model, deletion: Option.some({ id, text, busy: true }) },
        commands: [DeleteTask({ ...workstream(model), id })],
      };
    },
    TaskDeleted: ({ id }) => {
      const deleted = {
        ...model,
        tasks: Option.map(model.tasks, (tasks) => tasks.filter((task) => task.id !== id)),
        deletion: Option.none(),
      };
      return Option.exists(model.opened, (opened) => opened === id)
        ? leave(deleted)
        : { model: deleted };
    },
    DeleteFailed: () => ({ model: { ...model, deletion: Option.none() } }),
    DismissedDelete: () => ({ model: { ...model, deletion: Option.none() } }),
    PressedEscape: () => ({ model: { ...model, deletion: Option.none() } }),
    CompletedFocusDialog: () => ({ model }),
  });
}
