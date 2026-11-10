import { Option, Schema } from "effect";
import { HarnessChoices, type RoleName } from "../schemas";
import { Choice, Role } from "./schemas";

export const Flags = Schema.Struct({});

// The sheet of a role keeps the values of the form. context and max are the text of the two number fields.
export const Sheet = Schema.Struct({
  role: Role,
  harness: Schema.String,
  model: Schema.String,
  effort: Schema.String,
  context: Schema.String,
  max: Schema.String,
  openChoice: Schema.Option(Choice),
  error: Schema.String,
  saving: Schema.Boolean,
});

export const Model = Schema.Struct({
  roles: Schema.Option(Schema.Array(Role)),
  harnesses: Schema.Option(Schema.Array(HarnessChoices)),
  error: Schema.String,
  sheet: Schema.Option(Sheet),
});

export type Flags = Schema.Schema.Type<typeof Flags>;
export type Sheet = Schema.Schema.Type<typeof Sheet>;
export type Model = Schema.Schema.Type<typeof Model>;

export const initialModel: Model = {
  roles: Option.none(),
  harnesses: Option.none(),
  error: "",
  sheet: Option.none(),
};

export const roleName = (role: Role) => role.role.charAt(0).toUpperCase() + role.role.slice(1);

export const roleDescriptions: Partial<Record<RoleName, string>> = {
  lead: "Plans the tasks of one Workstream, talks with you in its chat, and approves the pull requests.",
  coordinator: "Talks with you in the Coordinator chat and puts your requests into Workstreams.",
  triager:
    "Discusses each issue with no Workstream with you, and proposes a Workstream or a message to a Lead.",
  implementer:
    "Writes the code of a task, and does the fix rounds, CI fix rounds and conflict rounds of its pull request.",
  researcher: "Answers a question about the code of a repository, and changes nothing.",
  reviewer: "Reviews each new head of a pull request and gives its findings.",
  judge: "Sorts the new comments on a pull request into fixes, questions, follow-ups and rejects.",
  curator:
    "Keeps the memory file of each repository up to date from the notes of the Leads and the sessions.",
  diagnoser: "Reads the job logs of a failed CI run and finds the cause.",
  operator:
    "Does work outside code for one Workstream, through the MCP servers of the Workstream, and reports to the Lead.",
};

export const hasContextLimit = (harness: string) => harness === "claude-code";

export const formatContext = (tokens: number) =>
  tokens >= 1000 && tokens % 1000 === 0 ? `${tokens / 1000}k` : String(tokens);

export const harnessNames = (model: Model) =>
  Option.getOrElse(model.harnesses, () => []).map((other) => other.harness);

export const modelsOf = (model: Model, harness: string) =>
  Option.getOrElse(model.harnesses, () => []).find((other) => other.harness === harness)?.models ??
  [];

// With no model list, the effort of the role stays the only effort.
export function effortsOf(model: Model, sheet: Sheet) {
  const chosen = modelsOf(model, sheet.harness).find((other) => other.model === sheet.model);
  if (chosen) {
    return chosen.efforts;
  }
  return sheet.effort ? [sheet.effort] : [];
}

export const sheetChanged = (sheet: Sheet) => Object.values(sheet.role.ui).some(Boolean);
