import { Option, Schema } from "effect";
import { lowLoadReason } from "@/lib/agents";
import { Agent, Line } from "./schemas";

export const Model = Schema.Struct({
  agent: Agent,
  lines: Schema.Option(Schema.Array(Line)),
  // older is true when the session has rows before the first loaded line.
  older: Schema.Boolean,
  loadingOlder: Schema.Boolean,
  // The id of the last line that the log got, to find the lines that came while the connection was down.
  newest: Schema.Option(Schema.Number),
  error: Schema.String,
  // The version changes with the lines, so the scroll Mount sees each change of the lines.
  version: Schema.Number,
  behind: Schema.Boolean,
  jumps: Schema.Number,
  opened: Schema.Boolean,
  // The ids of the lines that the Owner opened or closed against the default.
  toggledOpen: Schema.Array(Schema.Number),
  rawShown: Schema.Array(Schema.Number),
  startable: Schema.Boolean,
  starting: Schema.Boolean,
  startError: Schema.String,
});

export type Model = Schema.Schema.Type<typeof Model>;

export function initialModel(agent: Agent): Model {
  return {
    agent,
    lines: Option.none(),
    older: false,
    loadingOlder: false,
    newest: Option.none(),
    error: "",
    version: 0,
    behind: false,
    jumps: 0,
    opened: false,
    toggledOpen: [],
    rawShown: [],
    startable: agent.queueReason === lowLoadReason,
    starting: false,
    startError: "",
  };
}
