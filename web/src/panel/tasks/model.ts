import { Option, Schema } from "effect";
import { GetTaskResponse, ListTasksResponse } from "@/api/schemas.gen";
import * as Composer from "@/composer";
import { chatDraftKey } from "@/lib/draft";
import { displayState } from "@/lib/state";
import * as Setting from "@/setting/model";
import { NextSetting } from "@/setting/schemas";
import { Model as TextSelection } from "@/ui/selection";

export const TaskLine = ListTasksResponse.fields.data.value;
export type TaskLine = typeof TaskLine.Type;

export const TaskDetail = GetTaskResponse.fields.data;
export type TaskDetail = typeof TaskDetail.Type;

export const TargetTask = Schema.Struct({
  repository: Schema.String,
  number: Schema.Number,
});

export type TargetTask = typeof TargetTask.Type;

// Reads "<owner>/<name>/<number>".
export function parseTargetTask(text: string): Option.Option<TargetTask> {
  const match = /^(.+\/.+)\/(\d+)$/.exec(text);
  return match ? Option.some({ repository: match[1], number: Number(match[2]) }) : Option.none();
}

// The state of the buttons of a task. The key of a task changes with its state, so the state of the buttons ends with
// the state of the task.
export const Row = Schema.Struct({
  key: Schema.String,
  starting: Schema.Boolean,
  stopping: Schema.Boolean,
  error: Schema.String,
});

export type Row = typeof Row.Type;

export const Model = Schema.Struct({
  owner: Schema.String,
  name: Schema.String,
  number: Schema.Number,
  instance: Schema.String,
  lines: Schema.Option(Schema.Array(TaskLine)),
  fallback: Schema.Option(NextSetting),
  error: Schema.String,
  showClosed: Schema.Boolean,
  folded: Schema.Array(Schema.String),
  rows: Schema.Array(Row),
  sheet: Schema.Option(Setting.Model),
  opened: Schema.Option(TargetTask),
  pendingOpen: Schema.Option(TargetTask),
  scroll: Schema.Number,
  detail: Schema.Option(TaskDetail),
  detailLoading: Schema.Boolean,
  detailError: Schema.String,
  composer: Composer.Model,
  taskAttached: Schema.Boolean,
  selection: TextSelection,
});

export type Model = Schema.Schema.Type<typeof Model>;

export function initialModel(options: {
  owner: string;
  name: string;
  number: number;
  instance: string;
}): Model {
  return {
    ...options,
    lines: Option.none(),
    fallback: Option.none(),
    error: "",
    showClosed: false,
    folded: [],
    rows: [],
    sheet: Option.none(),
    opened: Option.none(),
    pendingOpen: Option.none(),
    scroll: 0,
    detail: Option.none(),
    detailLoading: false,
    detailError: "",
    composer: Composer.initialModel({
      draftKey: chatDraftKey(options.owner, `${options.owner}/${options.name}`, options.number),
      instance: `${options.instance}-task`,
      label: "Message to the Lead",
      placeholder: "Write to the Lead",
      organization: options.owner,
      repository: `${options.owner}/${options.name}`,
      workstream: options.number,
    }),
    taskAttached: false,
    selection: null,
  };
}

export const rowKey = (line: TaskLine) => `${line.url} ${line.state} ${line.wait}`;

export const rowOf = (model: Model, key: string): Row =>
  model.rows.find((row) => row.key === key) ?? {
    key,
    starting: false,
    stopping: false,
    error: "",
  };

export const withRow = (model: Model, key: string, change: Partial<Row>): Model => ({
  ...model,
  rows: [...model.rows.filter((row) => row.key !== key), { ...rowOf(model, key), ...change }],
});

export const resumes = (line: TaskLine) => line.state === "needs-human";

export const stops = (line: TaskLine) =>
  line.wait !== "" ||
  ["ready", "start waits", "working", "review", "question"].includes(line.state);

export const reference = (line: TaskLine) =>
  `${line.otherRepository ? line.repository : ""}#${line.number}`;

export const taskItem = (line: TaskLine) => ({
  id: line.url,
  reference: reference(line),
  title: line.title,
  reason: displayState(line).text,
  link: line.url,
});

export const openedLine = (model: Model): Option.Option<TaskLine> =>
  Option.flatMap(model.opened, (target) =>
    Option.flatMap(model.lines, (lines) =>
      Option.fromNullishOr(
        lines.find(
          (line) => line.repository === target.repository && line.number === target.number,
        ),
      ),
    ),
  );

// Removes the closed tasks. A sub-issue of a removed task moves up one level for each removed ancestor.
export function withoutClosed(lines: readonly TaskLine[]): TaskLine[] {
  const removed: number[] = [];
  const open: TaskLine[] = [];
  for (const line of lines) {
    while (removed.length > 0 && removed[removed.length - 1] >= line.depth) {
      removed.pop();
    }
    if (line.state === "closed") {
      removed.push(line.depth);
    } else {
      open.push({ ...line, depth: line.depth - removed.length });
    }
  }
  return open;
}

export const isProject = (line: TaskLine) => line.kind === "project";

// The lines after index that are below the line at index.
const below = (lines: readonly TaskLine[], index: number): TaskLine[] => {
  const end = lines.findIndex((line, i) => i > index && line.depth <= lines[index].depth);
  return lines.slice(index + 1, end === -1 ? lines.length : end);
};

export type Progress = { done: number; all: number };

// The count of the task lines below each Project at each depth, by the URL of the Project.
export function progressOfProjects(lines: readonly TaskLine[]): Map<string, Progress> {
  const progress = new Map<string, Progress>();
  lines.forEach((line, index) => {
    if (isProject(line)) {
      const tasks = below(lines, index).filter((task) => !isProject(task));
      progress.set(line.url, {
        done: tasks.filter((task) => task.state === "closed").length,
        all: tasks.length,
      });
    }
  });
  return progress;
}

// Puts the lines that have no Project ancestor first. Then each outer Project follows with its lines.
export function groupByProject(lines: readonly TaskLine[]): TaskLine[] {
  const loose: TaskLine[] = [];
  const blocks: TaskLine[] = [];
  let index = 0;
  while (index < lines.length) {
    if (isProject(lines[index])) {
      const block = [lines[index], ...below(lines, index)];
      blocks.push(...block);
      index += block.length;
    } else {
      loose.push(lines[index]);
      index += 1;
    }
  }
  return [...loose, ...blocks];
}

// Removes the lines below the folded Projects.
export function withoutFolded(lines: readonly TaskLine[], folded: readonly string[]): TaskLine[] {
  const shown: TaskLine[] = [];
  let index = 0;
  while (index < lines.length) {
    shown.push(lines[index]);
    index += folded.includes(lines[index].url) ? below(lines, index).length + 1 : 1;
  }
  return shown;
}
