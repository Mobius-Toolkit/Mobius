import { Option, Schema } from "effect";
import type { Flags } from "./flags";
import { NeedsHuman, Unread, WaitingWorkstream, Workstream } from "./schemas";

export const Model = Schema.Struct({
  organization: Schema.String,
  workstreams: Schema.Option(Schema.Array(Workstream)),
  waiting: Schema.Array(WaitingWorkstream),
  working: Schema.Array(Schema.String),
  error: Schema.String,
  needsHuman: Schema.Array(NeedsHuman),
  unread: Schema.Array(Unread),
});

export type Model = Schema.Schema.Type<typeof Model>;

export function initialModel(flags: Flags): Model {
  return {
    organization: flags.organization,
    workstreams: Option.none(),
    waiting: [],
    working: [],
    error: "",
    needsHuman: [],
    unread: [],
  };
}
