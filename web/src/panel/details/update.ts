import { Option } from "effect";
import { Command } from "foldkit";
import * as Update from "foldkit/update";
import { OutMessage as SettingOutMessage } from "@/setting/message";
import { init as initSetting, update as updateSetting } from "@/setting/update";
import { CloseWorkstream, FetchClosure, FetchDetails, FetchRoles, SetEvergreen } from "./commands";
import { Message, OutMessage, toGotSettingMessage } from "./message";
import { sessionTitle, type Model, type Session } from "./model";

type UpdateReturn = Update.ReturnWithOutMessage<Model, Message, OutMessage>;

const workstream = (model: Model) => ({
  owner: model.owner,
  name: model.name,
  number: model.number,
});

export const reload = (model: Model) => [FetchDetails(workstream(model)), FetchRoles()];

export const refresh = (model: Model): Update.Return<Model, Message> => ({
  model,
  commands: reload(model),
});

const foldSetting = Update.foldChild({
  update: updateSetting,
  read: (model: Model) => Option.map(model.sheet, (sheet) => sheet.setting),
  write: (model, setting) => ({
    ...model,
    sheet: Option.map(model.sheet, (sheet) => ({ ...sheet, setting })),
  }),
  toParentMessage: toGotSettingMessage,
  foldOutMessage: (outMessage) =>
    SettingOutMessage.match<Update.StepWithOutMessage<Model, Message, OutMessage>>(outMessage, {
      Changed:
        ({ setting }) =>
        (model) => ({ model: changed(model, setting) }),
      Closed: () => (model) => ({ model: { ...model, sheet: Option.none() } }),
      LoginRequired: () => (model) => ({ model, outMessage: OutMessage.LoginRequired() }),
    }),
});

// A setting of the Workstream replaces the binding of the session, and a cleared setting gives the binding of the Role
// back.
function changed(
  model: Model,
  setting: Option.Option<{ harness: string; model: string; effort: string }>,
): Model {
  if (Option.isNone(model.sheet)) {
    return model;
  }
  const session = model.sheet.value.session;
  const role = Option.flatMapNullishOr(model.roles, (roles) =>
    roles.find((other) => other.role === session),
  );
  const next = Option.isSome(setting)
    ? Option.some({ ...setting.value, own: true })
    : Option.map(role, ({ harness, model: modelName, effort }) => ({
        harness,
        model: modelName,
        effort,
        own: false,
      }));
  return {
    ...model,
    sheet: Option.none(),
    details: Option.map(model.details, (details) =>
      Option.match(next, {
        onNone: () => details,
        onSome: (value) => ({ ...details, [session]: value }),
      }),
    ),
  };
}

const sessionTarget = {
  lead: "Lead",
  implementer: "WorkstreamImplementer",
  operator: "Operator",
} as const satisfies Record<Session, string>;

function openSheet(model: Model, session: Session): UpdateReturn {
  const role = Option.flatMapNullishOr(model.roles, (roles) =>
    roles.find((other) => other.role === session),
  );
  if (Option.isNone(model.details) || Option.isNone(role)) {
    return { model };
  }
  const current = model.details.value[session];
  const { model: setting, commands } = initSetting({
    title: sessionTitle[session],
    target: { _tag: sessionTarget[session], ...workstream(model) },
    own: current.own
      ? Option.some({ harness: current.harness, model: current.model, effort: current.effort })
      : Option.none(),
    fallback: {
      harness: role.value.harness,
      model: role.value.model,
      effort: role.value.effort,
    },
    fallbackSource: "role",
  });
  return {
    model: { ...model, sheet: Option.some({ session, setting }) },
    commands: Command.mapMessages(commands, toGotSettingMessage),
  };
}

export function update(model: Model, message: Message): UpdateReturn {
  return Message.match<UpdateReturn>(message, {
    DetailsLoaded: ({ details }) => ({ model: { ...model, details: Option.some(details) } }),
    RolesLoaded: ({ roles }) => ({ model: { ...model, roles: Option.some(roles) } }),
    LoadFailed: ({ error }) => ({ model: { ...model, error } }),
    LoginRequired: () => ({
      model: { ...model, evergreenBusy: false },
      outMessage: OutMessage.LoginRequired(),
    }),
    SwitchedEvergreen: ({ on }) => ({
      model: { ...model, evergreenBusy: true, error: "" },
      commands: [SetEvergreen({ ...workstream(model), on })],
    }),
    EvergreenChanged: ({ on }) => ({
      model: {
        ...model,
        evergreenBusy: false,
        details: Option.map(model.details, (details) => ({ ...details, evergreen: on })),
      },
    }),
    EvergreenFailed: ({ error }) => ({ model: { ...model, evergreenBusy: false, error } }),
    ClickedChange: ({ session }) => openSheet(model, session),
    ClickedClose: () => ({
      model: { ...model, closure: Option.some({ items: Option.none(), error: "", busy: false }) },
      commands: [FetchClosure(workstream(model))],
    }),
    ClosureLoaded: ({ items }) => ({
      model: {
        ...model,
        closure: Option.map(model.closure, (closure) => ({
          ...closure,
          items: Option.some(items),
        })),
      },
    }),
    ClosureFailed: ({ error }) => ({
      model: { ...model, closure: Option.map(model.closure, (closure) => ({ ...closure, error })) },
    }),
    ClickedCloseWorkstream: () => ({
      model: {
        ...model,
        closure: Option.map(model.closure, (closure) => ({ ...closure, busy: true, error: "" })),
      },
      commands: [CloseWorkstream(workstream(model))],
    }),
    WorkstreamClosed: () => ({ model }),
    CloseFailed: ({ error }) => ({
      model: {
        ...model,
        closure: Option.map(model.closure, (closure) => ({ ...closure, busy: false, error })),
      },
    }),
    DismissedClosure: () => ({ model: { ...model, closure: Option.none() } }),
    PressedEscape: () => ({ model: { ...model, closure: Option.none() } }),
    CompletedFocusClosure: () => ({ model }),
    GotSettingMessage: ({ message: child }) => foldSetting(model, child),
  });
}
