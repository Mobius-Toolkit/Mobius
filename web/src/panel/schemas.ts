import { Schema } from "effect";
import { Session } from "./details/model";
import { TargetTask } from "./tasks/model";

export const Tab = Schema.Literals(["tasks", "agents", "details", "brief", "recurring"]);

export type Tab = typeof Tab.Type;

// The setting sheet that opens as soon as the data of its tab loads. A task opens the sheet of that task, and no task
// opens the sheet of the Workstream.
export const Opening = Schema.Struct({
  setting: Session,
  task: Schema.Option(TargetTask),
});

export type Opening = typeof Opening.Type;
