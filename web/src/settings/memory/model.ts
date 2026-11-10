import { Option, Schema } from "effect";
import { briefMarkdown } from "@/lib/briefDiff";
import { diffHunks } from "@/lib/diff";
import { GitHubApp, MemoryFile, MemoryVersion } from "../schemas";

// The page "repositories" lists the repositories of each organization, "files" lists the memory files of one
// repository, and "file" shows one memory file with its versions. owner, name and file are empty on the pages that have
// no such part.
export const Page = Schema.Literals(["repositories", "files", "file"]);

export const Flags = Schema.Struct({
  page: Page,
  owner: Schema.String,
  name: Schema.String,
  file: Schema.String,
});

// hunks is the Markdown of each changed part of the version, with the marks of the diff.
export const ShownVersion = Schema.Struct({
  ...MemoryVersion.fields,
  hunks: Schema.Array(Schema.String),
});

// discarding is true while the dialog asks the Owner to confirm the discard of the draft.
export const Editing = Schema.Struct({
  draft: Schema.String,
  base: Schema.Number,
  saving: Schema.Boolean,
  error: Schema.String,
  discarding: Schema.Boolean,
});

export const Model = Schema.Struct({
  page: Page,
  owner: Schema.String,
  name: Schema.String,
  file: Schema.String,
  apps: Schema.Array(GitHubApp),
  organizations: Schema.Array(Schema.String),
  files: Schema.Option(Schema.Array(MemoryFile)),
  versions: Schema.Option(Schema.Array(ShownVersion)),
  older: Schema.Boolean,
  loadingOlder: Schema.Boolean,
  error: Schema.String,
  editing: Schema.Option(Editing),
  reverting: Schema.Option(Schema.Number),
});

export type Page = Schema.Schema.Type<typeof Page>;
export type Flags = Schema.Schema.Type<typeof Flags>;
export type ShownVersion = Schema.Schema.Type<typeof ShownVersion>;
export type Editing = Schema.Schema.Type<typeof Editing>;
export type Model = Schema.Schema.Type<typeof Model>;

export const initialModel = (flags: Flags): Model => ({
  ...flags,
  apps: [],
  organizations: [],
  files: Option.none(),
  versions: Option.none(),
  older: false,
  loadingOlder: false,
  error: "",
  editing: Option.none(),
  reverting: Option.none(),
});

const fileTitles: Record<string, string> = {
  shared: "Shared",
  lead: "Lead",
  implementer: "Implementer",
  reviewer: "Reviewer",
  judge: "Judge",
  researcher: "Researcher",
  diagnoser: "Diagnoser",
  curator: "Curator",
};

export const fileTitle = (file: string) => fileTitles[file] ?? file;

export function pageTitle(flags: Flags) {
  switch (flags.page) {
    case "repositories":
      return "Memory";
    case "files":
      return `Memory of ${flags.owner}/${flags.name}`;
    case "file":
      return `${fileTitle(flags.file)} · ${flags.owner}/${flags.name}`;
  }
}

export const lineText = (lines: number) =>
  lines === 0 ? "Empty" : lines === 1 ? "1 line" : `${lines} lines`;

export const shown = (version: MemoryVersion): ShownVersion => ({
  ...version,
  hunks: diffHunks(version.before, version.text).map((hunk) => briefMarkdown(hunk)),
});

export const versionsOf = (model: Model) => Option.getOrElse(model.versions, () => []);

export const currentText = (model: Model) => versionsOf(model)[0]?.text ?? "";

// The id of the newest version is 0 for a file with no version.
export const newestId = (model: Model) => versionsOf(model)[0]?.id ?? 0;

export function repositoriesOf(model: Model, organization: string) {
  return model.apps
    .flatMap((app) => app.repositories)
    .filter((repository) => repository.startsWith(`${organization}/`));
}
