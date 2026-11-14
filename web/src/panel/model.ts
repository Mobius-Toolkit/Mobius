import { Option, Schema } from "effect";
import * as Agents from "./agents/model";
import type { Flags } from "./flags";
import * as Details from "./details/model";
import * as Recurring from "./recurring/model";
import { Model as TextSelection } from "@/ui/selection";
import { Opening, Tab } from "./schemas";
import * as Tasks from "./tasks/model";

export const Model = Schema.Struct({
  owner: Schema.String,
  name: Schema.String,
  number: Schema.Number,
  instance: Schema.String,
  tab: Tab,
  opening: Schema.Option(Opening),
  brief: Schema.String,
  selection: TextSelection,
  tasks: Tasks.Model,
  agents: Agents.Model,
  details: Details.Model,
  recurring: Recurring.Model,
});

export type Model = Schema.Schema.Type<typeof Model>;

export function initialModel(flags: Flags): Model {
  const workstream = {
    owner: flags.owner,
    name: flags.name,
    number: flags.number,
    instance: flags.instance,
  };
  return {
    ...workstream,
    tab: flags.tab,
    opening: flags.opening,
    brief: "",
    selection: null,
    tasks: Tasks.initialModel(workstream),
    agents: Agents.initialModel(workstream),
    details: Details.initialModel(workstream),
    recurring: Recurring.initialModel(workstream),
  };
}

// A sheet or a dialog of the panel takes the first Escape.
export function hasOpenOverlay(model: Model): boolean {
  return (
    Option.isSome(model.tasks.sheet) ||
    Option.isSome(model.details.sheet) ||
    Option.isSome(model.details.closure) ||
    Option.isSome(model.details.mcpSheet) ||
    Option.isSome(model.recurring.deletion)
  );
}
