import { Schema } from "effect";
import * as Agents from "./agents/model";
import type { Flags } from "./flags";
import * as Details from "./details/model";
import * as Recurring from "./recurring/model";
import { Tab } from "./schemas";
import * as Tasks from "./tasks/model";

export const Model = Schema.Struct({
  owner: Schema.String,
  name: Schema.String,
  number: Schema.Number,
  instance: Schema.String,
  tab: Tab,
  brief: Schema.String,
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
    brief: "",
    tasks: Tasks.initialModel(workstream),
    agents: Agents.initialModel(workstream),
    details: Details.initialModel(workstream),
    recurring: Recurring.initialModel(workstream),
  };
}
