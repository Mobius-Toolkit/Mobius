import { Option } from "effect";
import * as Update from "foldkit/update";
import { DeleteTask, FetchTasks, SetPaused } from "./commands";
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

export function update(model: Model, message: Message): UpdateReturn {
  return Message.match<UpdateReturn>(message, {
    TasksLoaded: ({ tasks }) => ({
      model: {
        ...model,
        tasks: Option.some(tasks),
        error: "",
        opened: Option.filter(model.opened, (id) => tasks.some((task) => task.id === id)),
      },
    }),
    LoadFailed: ({ error }) => ({ model: { ...model, error } }),
    LoginRequired: () => ({
      model: {
        ...model,
        busy: [],
        deletion: Option.map(model.deletion, (deletion) => ({ ...deletion, busy: false })),
      },
      outMessage: OutMessage.LoginRequired(),
    }),
    ClickedRow: ({ id }) => ({
      model: {
        ...model,
        opened: Option.exists(model.opened, (opened) => opened === id)
          ? Option.none()
          : Option.some(id),
      },
    }),
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
    TaskDeleted: ({ id }) => ({
      model: {
        ...model,
        tasks: Option.map(model.tasks, (tasks) => tasks.filter((task) => task.id !== id)),
        opened: Option.filter(model.opened, (opened) => opened !== id),
        deletion: Option.none(),
      },
    }),
    DeleteFailed: () => ({ model: { ...model, deletion: Option.none() } }),
    DismissedDelete: () => ({ model: { ...model, deletion: Option.none() } }),
    PressedEscape: () => ({ model: { ...model, deletion: Option.none() } }),
    CompletedFocusDialog: () => ({ model }),
  });
}
