import { Option } from "effect";
import { Command } from "foldkit";
import * as Update from "foldkit/update";
import { OutMessage as SettingOutMessage } from "@/setting/message";
import type { Target } from "@/setting/schemas";
import { init as initSetting, update as updateSetting } from "@/setting/update";
import { chatDraftKey } from "@/lib/draft";
import { AttachTask, ChangeIssue, FetchFallback, FetchTasks } from "./commands";
import { Message, OutMessage, toGotSettingMessage } from "./message";
import { reference, resumes, withRow, type Model, type TaskLine } from "./model";

type UpdateReturn = Update.ReturnWithOutMessage<Model, Message, OutMessage>;

export const reload = (model: Model) => [
  FetchTasks({ owner: model.owner, name: model.name, number: model.number }),
  FetchFallback({ owner: model.owner, name: model.name, number: model.number }),
];

export const refresh = (model: Model): Update.Return<Model, Message> => ({
  model,
  commands: reload(model),
});

const foldSetting = Update.foldChild({
  update: updateSetting,
  read: (model: Model) => model.sheet,
  write: (model, sheet) => ({ ...model, sheet: Option.some(sheet) }),
  toParentMessage: toGotSettingMessage,
  foldOutMessage: (outMessage) =>
    SettingOutMessage.match<Update.StepWithOutMessage<Model, Message, OutMessage>>(outMessage, {
      Changed:
        ({ setting }) =>
        (model) => ({
          model: {
            ...model,
            sheet: Option.none(),
            lines: Option.map(model.lines, (lines) =>
              lines.map((line) =>
                Option.exists(model.sheet, (sheet) => isTarget(sheet.target, line))
                  ? { ...line, implementer: Option.getOrNull(setting) }
                  : line,
              ),
            ),
          },
        }),
      Closed: () => (model) => ({ model: { ...model, sheet: Option.none() } }),
      LoginRequired: () => (model) => ({ model, outMessage: OutMessage.LoginRequired() }),
    }),
});

export function update(model: Model, message: Message): UpdateReturn {
  return Message.match<UpdateReturn>(message, {
    TasksLoaded: ({ lines }) => ({ model: { ...model, lines: Option.some(lines) } }),
    FallbackLoaded: ({ fallback }) => ({ model: { ...model, fallback: Option.some(fallback) } }),
    LoadFailed: ({ error }) => ({ model: { ...model, error } }),
    LoginRequired: () => ({ model, outMessage: OutMessage.LoginRequired() }),
    ToggledClosed: () => ({ model: { ...model, showClosed: !model.showClosed } }),
    ClickedStart: ({ key, line }) => change(model, key, line, startAction(line)),
    ClickedStop: ({ key, line }) => change(model, key, line, "stop"),
    ChangedIssue: ({ key }) => ({
      model: withRow(model, key, { starting: false, stopping: false, error: "" }),
    }),
    ChangeFailed: ({ key, error }) => ({
      model: withRow(model, key, { starting: false, stopping: false, error }),
    }),
    ClickedChat: ({ key, line }) => attach(model, key, line),
    AttachedTask: () => ({ model, outMessage: OutMessage.TaskAttached() }),
    AttachFailed: ({ key, error }) => ({ model: withRow(model, key, { error }) }),
    ClickedSetting: ({ line }) => openSheet(model, line),
    GotSettingMessage: ({ message: child }) => foldSetting(model, child),
  });
}

const startAction = (line: TaskLine) => {
  if (resumes(line)) {
    return "resume";
  }
  return line.blockedBy.length > 0 ? "start-blocked" : "start";
};

function change(
  model: Model,
  key: string,
  line: TaskLine,
  action: "start" | "start-blocked" | "resume" | "stop",
): UpdateReturn {
  const [owner, name] = line.repository.split("/");
  return {
    model: withRow(model, key, {
      starting: action !== "stop",
      stopping: action === "stop",
      error: "",
    }),
    commands: [
      ChangeIssue({ key, action, owner, name, number: line.number, reference: reference(line) }),
    ],
  };
}

function attach(model: Model, key: string, line: TaskLine): UpdateReturn {
  const draftKey = chatDraftKey(model.owner, `${model.owner}/${model.name}`, model.number);
  const item = {
    id: line.url,
    reference: reference(line),
    title: line.title,
    reason: line.state,
    link: line.url,
  };
  return {
    model: withRow(model, key, { error: "" }),
    commands: [AttachTask({ key, draftKey, item })],
  };
}

function openSheet(model: Model, line: TaskLine): UpdateReturn {
  if (Option.isNone(model.fallback)) {
    return { model };
  }
  const [owner, name] = line.repository.split("/");
  const fallback = model.fallback.value;
  const { model: sheet, commands } = initSetting({
    title: `Implementer of ${reference(line)}`,
    target: { _tag: "Task", owner, name, number: line.number },
    own: Option.fromNullishOr(line.implementer).pipe(
      Option.map(({ harness, model: modelName, effort }) => ({
        harness,
        model: modelName,
        effort,
      })),
    ),
    fallback: { harness: fallback.harness, model: fallback.model, effort: fallback.effort },
    fallbackSource: fallback.own ? "Workstream" : "role",
  });
  return {
    model: { ...model, sheet: Option.some(sheet) },
    commands: Command.mapMessages(commands, toGotSettingMessage),
  };
}

function isTarget(target: Target, line: TaskLine) {
  return line.repository === `${target.owner}/${target.name}` && line.number === target.number;
}
