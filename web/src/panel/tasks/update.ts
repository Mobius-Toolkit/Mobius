import { Option } from "effect";
import { Command } from "foldkit";
import * as Update from "foldkit/update";
import * as Composer from "@/composer";
import { OutMessage as ComposerOutMessage } from "@/composer/message";
import { OutMessage as SettingOutMessage } from "@/setting/message";
import type { Target } from "@/setting/schemas";
import { init as initSetting, update as updateSetting } from "@/setting/update";
import { chatDraftKey } from "@/lib/draft";
import {
  AttachTask,
  ChangeIssue,
  FetchFallback,
  FetchTaskDetail,
  FetchTasks,
  OpenTask,
  RestoreScroll,
} from "./commands";
import { Message, OutMessage, toGotComposerMessage, toGotSettingMessage } from "./message";
import {
  openedLine,
  reference,
  resumes,
  taskItem,
  withRow,
  type Model,
  type TargetTask,
  type TaskLine,
} from "./model";

type UpdateReturn = Update.ReturnWithOutMessage<Model, Message, OutMessage>;

export const reload = (model: Model) => [
  FetchTasks({ owner: model.owner, name: model.name, number: model.number }),
  FetchFallback({ owner: model.owner, name: model.name, number: model.number }),
];

export const refresh = (model: Model): Update.Return<Model, Message> => ({
  model,
  commands: reload(model),
});

export function openSetting(model: Model, line: TaskLine): Update.Return<Model, Message> {
  const { model: next, commands } = openSheet(model, line);
  return { model: next, commands };
}

// The view opens as soon as the list loads.
export function open(model: Model, target: TargetTask): Update.Return<Model, Message> {
  if (Option.isNone(model.lines)) {
    return { model: { ...model, pendingOpen: Option.some(target) } };
  }
  const { model: next, commands } = openTarget(model, target);
  return { model: next, commands };
}

const isOpened = (model: Model, repository: string, number: number) =>
  Option.isSome(model.opened) &&
  model.opened.value.repository === repository &&
  model.opened.value.number === number;

const leave = (model: Model): UpdateReturn => ({
  model: {
    ...model,
    opened: Option.none(),
    detail: Option.none(),
    detailLoading: false,
    detailError: "",
  },
  commands: [RestoreScroll({ instance: model.instance, scroll: model.scroll })],
});

// A task that the list does not have shows the list.
function openTarget(model: Model, target: TargetTask): UpdateReturn {
  const line = Option.flatMap(model.lines, (lines) =>
    Option.fromNullishOr(
      lines.find(
        (other) => other.repository === target.repository && other.number === target.number,
      ),
    ),
  );
  if (Option.isSome(line)) {
    return { model, commands: [OpenTask({ instance: model.instance, line: line.value })] };
  }
  return Option.isSome(model.opened) ? leave(model) : { model };
}

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

// The send is on the Lead chat: the message goes to the outbox, and the chat sends it.
const foldComposer = Update.foldChild({
  update: Composer.update,
  read: (model: Model) => Option.some(model.composer),
  write: (model, composer) => ({ ...model, composer }),
  toParentMessage: toGotComposerMessage,
  foldOutMessage: (outMessage) =>
    ComposerOutMessage.match<Update.Step<Model, Message>>(outMessage, {
      StopClicked: () => (model) => ({ model }),
      MessageEnqueued: () => (model) => ({ model }),
    }),
});

const foldAddInboxItem = Update.foldChild({
  update: Composer.addDraftInboxItem,
  read: (model: Model) => Option.some(model.composer),
  write: (model, composer) => ({ ...model, composer }),
  toParentMessage: toGotComposerMessage,
});

const draftChanges: readonly string[] = ["DraftTextChanged", "ImagesFitted", "VoiceButtonClicked"];

// The first change of the draft from the view attaches the opened task, once for each opening of the view.
const attachOpenedTask: Update.Step<Model, Message> = (model) => {
  const next = { ...model, taskAttached: true };
  const line = openedLine(model);
  if (
    model.taskAttached ||
    Option.isNone(line) ||
    model.composer.draftInboxItems.some((item) => item.link === line.value.url)
  ) {
    return { model: next };
  }
  return foldAddInboxItem(next, taskItem(line.value));
};

export function update(model: Model, message: Message): UpdateReturn {
  return Message.match<UpdateReturn>(message, {
    TasksLoaded: ({ lines }) => {
      const loaded = { ...model, lines: Option.some(lines) };
      if (Option.isSome(model.pendingOpen)) {
        return openTarget({ ...loaded, pendingOpen: Option.none() }, model.pendingOpen.value);
      }
      const stillInList = Option.match(model.opened, {
        onNone: () => true,
        onSome: (target) =>
          lines.some(
            (line) => line.repository === target.repository && line.number === target.number,
          ),
      });
      return stillInList ? { model: loaded } : leave(loaded);
    },
    FallbackLoaded: ({ fallback }) => ({ model: { ...model, fallback: Option.some(fallback) } }),
    LoadFailed: ({ error }) => ({ model: { ...model, error, pendingOpen: Option.none() } }),
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
    ClickedTask: ({ line }) => ({
      model,
      commands: [OpenTask({ instance: model.instance, line })],
    }),
    OpenedTask: ({ line, scroll }) => ({
      model: {
        ...model,
        opened: Option.some({ repository: line.repository, number: line.number }),
        scroll: Option.isSome(model.opened) ? model.scroll : scroll,
        detail: Option.none(),
        detailLoading: true,
        detailError: "",
        taskAttached: false,
      },
      commands: [
        RestoreScroll({ instance: model.instance, scroll: 0 }),
        FetchTaskDetail({
          owner: model.owner,
          name: model.name,
          workstream: model.number,
          repository: line.repository,
          number: line.number,
        }),
      ],
    }),
    ClickedBack: () => leave(model),
    GotComposerMessage: ({ message: child }) =>
      Update.combine(model, [
        ...(draftChanges.includes(child._tag) ? [attachOpenedTask] : []),
        foldComposer(child),
      ]),
    CompletedRestoreScroll: () => ({ model }),
    TaskDetailLoaded: ({ repository, number, detail }) => {
      if (!isOpened(model, repository, number)) {
        return { model };
      }
      return {
        model: {
          ...model,
          detail: Option.some(detail),
          detailLoading: false,
          detailError: "",
        },
      };
    },
    TaskDetailFailed: ({ repository, number, error }) => {
      if (!isOpened(model, repository, number)) {
        return { model };
      }
      return {
        model: {
          ...model,
          detailLoading: false,
          detailError: error,
        },
      };
    },
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
  return {
    model: withRow(model, key, { error: "" }),
    commands: [AttachTask({ key, draftKey, item: taskItem(line) })],
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
