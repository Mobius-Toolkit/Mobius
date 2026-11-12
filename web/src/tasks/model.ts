import { Option, Schema } from "effect";
import type { Flags } from "./flags";
import { BoardTask } from "./schemas";

export const Model = Schema.Struct({
  organization: Schema.String,
  tasks: Schema.Option(Schema.Array(BoardTask)),
  error: Schema.String,
  now: Schema.Number,
});

export type Model = Schema.Schema.Type<typeof Model>;

export function initialModel(flags: Flags): Model {
  return { organization: flags.organization, tasks: Option.none(), error: "", now: 0 };
}

type State = BoardTask["state"];

const inStates = (task: BoardTask, states: readonly State[]) => states.includes(task.state);

export const columns: { title: string; has: (task: BoardTask) => boolean }[] = [
  { title: "Queued", has: (task) => inStates(task, ["dispatched", "queued", "answered"]) },
  { title: "Working", has: (task) => inStates(task, ["working", "reviewed"]) },
  { title: "Waiting", has: (task) => inStates(task, ["checks", "approval"]) },
  {
    title: "Needs you",
    has: (task) => inStates(task, ["needs_human", "asked", "stopped", "owner_action"]),
  },
  { title: "Ready to merge", has: (task) => task.state === "ready_for_review" },
  { title: "Done", has: (task) => task.state === "ended" },
];

const byStateAt = (a: BoardTask, b: BoardTask) => Date.parse(a.stateAt) - Date.parse(b.stateAt);

const byQueue = (a: BoardTask, b: BoardTask) => {
  if (a.mergeQueuePlace === null || b.mergeQueuePlace === null) {
    return Number(a.mergeQueuePlace === null) - Number(b.mergeQueuePlace === null);
  }
  if (a.mergeQueuePlace !== b.mergeQueuePlace) {
    return a.mergeQueuePlace - b.mergeQueuePlace;
  }
  return a.repository < b.repository ? -1 : a.repository > b.repository ? 1 : 0;
};

export function sortColumn(title: string, tasks: readonly BoardTask[]): BoardTask[] {
  if (title === "Done") {
    return tasks.toSorted((a, b) => byStateAt(b, a));
  }
  if (title === "Waiting" || title === "Ready to merge") {
    return tasks.toSorted((a, b) => byQueue(a, b) || byStateAt(a, b));
  }
  return tasks.toSorted(byStateAt);
}
