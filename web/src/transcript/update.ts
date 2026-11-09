import { Option } from "effect";
import type { Return, ReturnWithOutMessage } from "foldkit/update";
import { FetchLog, FetchOlder, StartCheck } from "./commands";
import { Message, OutMessage } from "./message";
import type { Model } from "./model";
import type { Line } from "./schemas";

type UpdateReturn = ReturnWithOutMessage<Model, Message, OutMessage>;

// The agent only adds text to a chunk, so the longer raw text is the newer text.
export function merge(list: readonly Line[], added: readonly Line[]): Line[] {
  const lines = new Map(list.map((line) => [line.id, line]));
  for (const line of added) {
    const known = lines.get(line.id);
    if (!known || known.raw.length < line.raw.length) {
      lines.set(line.id, line);
    }
  }
  return [...lines.values()].toSorted((a, b) => a.id - b.id);
}

const withLines = (model: Model, added: readonly Line[]): Model => ({
  ...model,
  lines: Option.some(
    merge(
      Option.getOrElse(model.lines, () => []),
      added,
    ),
  ),
  version: model.version + 1,
});

const newestOf = (model: Model, added: readonly Line[]) =>
  added.length > 0
    ? Option.some(
        Math.max(
          Option.getOrElse(model.newest, () => 0),
          ...added.map((line) => line.id),
        ),
      )
    : model.newest;

const toggled = (ids: readonly number[], id: number) =>
  ids.includes(id) ? ids.filter((other) => other !== id) : [...ids, id];

export function update(model: Model, message: Message): UpdateReturn {
  return Message.match<UpdateReturn>(message, {
    StreamOpened: () => ({
      model: { ...model, opened: true },
      commands: [FetchLog({ id: model.agent.id, known: model.newest })],
    }),
    StreamClosed: () => ({ model: { ...model, opened: false } }),
    StreamRetried: () => ({
      model,
      commands: [FetchLog({ id: model.agent.id, known: model.newest })],
    }),
    LineReceived: ({ line }) => {
      const first = Option.flatMapNullishOr(model.lines, (lines) => lines[0]);
      const gap = Option.isSome(first) && line.id < first.value.id;
      const next = { ...model, newest: newestOf(model, [line]) };
      return { model: gap ? next : withLines(next, [line]) };
    },
    Loaded: ({ lines, older, initial }) => ({
      model: {
        ...withLines(model, lines),
        newest: newestOf(model, lines),
        older: initial ? older : model.older,
      },
    }),
    OlderLoaded: ({ lines, older }) => ({
      model: { ...withLines(model, lines), older, loadingOlder: false },
    }),
    LoadFailed: ({ error }) => ({ model: { ...model, error, loadingOlder: false } }),
    LoginRequired: () => ({
      model: { ...model, loadingOlder: false, starting: false },
      outMessage: OutMessage.LoginRequired(),
    }),
    NearTop: () => {
      const first = Option.flatMapNullishOr(model.lines, (lines) => lines[0]);
      if (!model.older || model.loadingOlder || Option.isNone(first)) {
        return { model };
      }
      return {
        model: { ...model, loadingOlder: true },
        commands: [FetchOlder({ id: model.agent.id, before: first.value.id })],
      };
    },
    ArrivedWhileAway: () => ({ model: { ...model, behind: true } }),
    ReachedEnd: () => ({ model: { ...model, behind: false } }),
    JumpedToEnd: () => ({ model: { ...model, behind: false, jumps: model.jumps + 1 } }),
    ToggledOpen: ({ id }) => ({ model: { ...model, toggledOpen: toggled(model.toggledOpen, id) } }),
    ToggledRaw: ({ id }) => ({ model: { ...model, rawShown: toggled(model.rawShown, id) } }),
    ClickedBack: () => ({ model, outMessage: OutMessage.Closed() }),
    ClickedStartCheck: () => ({
      model: { ...model, starting: true },
      commands: [StartCheck({ id: model.agent.id })],
    }),
    StartedCheck: () => ({ model: { ...model, starting: false, startError: "" } }),
    StartCheckFailed: ({ error }) => ({ model: { ...model, starting: false, startError: error } }),
  });
}

// The control that starts the check ends with the wait, so its state ends too.
export function reflectStartable(model: Model, startable: boolean): Return<Model, Message> {
  return {
    model: startable
      ? { ...model, startable }
      : { ...model, startable, starting: false, startError: "" },
  };
}
