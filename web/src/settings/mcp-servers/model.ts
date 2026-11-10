import { Option, Schema } from "effect";
import { Callback, Kind, McpServer, type Pair } from "./schemas";

export const Flags = Schema.Struct({ organization: Schema.String, callback: Callback });

// stored is true for a row whose value the server keeps. The sheet never gets that value.
export const SheetPair = Schema.Struct({
  name: Schema.String,
  value: Schema.String,
  stored: Schema.Boolean,
});

export const Pending = Schema.Literals(["none", "save", "delete", "connect", "disconnect"]);

// The sheet of a server keeps the values of the form. id is none for a new server, and args is one argument on each line.
export const Sheet = Schema.Struct({
  id: Schema.Option(Schema.Number),
  name: Schema.String,
  kind: Kind,
  url: Schema.String,
  command: Schema.String,
  args: Schema.String,
  headers: Schema.Array(SheetPair),
  env: Schema.Array(SheetPair),
  clientId: Schema.String,
  clientSecret: Schema.String,
  confirmingDelete: Schema.Boolean,
  error: Schema.String,
  pending: Pending,
});

export const Model = Schema.Struct({
  organization: Schema.String,
  servers: Schema.Option(Schema.Array(McpServer)),
  callback: Schema.Option(Callback),
  error: Schema.String,
  sheet: Schema.Option(Sheet),
});

export type Flags = Schema.Schema.Type<typeof Flags>;
export type SheetPair = Schema.Schema.Type<typeof SheetPair>;
export type Pending = Schema.Schema.Type<typeof Pending>;
export type Sheet = Schema.Schema.Type<typeof Sheet>;
export type Model = Schema.Schema.Type<typeof Model>;

export const initialModel = (flags: Flags): Model => ({
  organization: flags.organization,
  servers: Option.none(),
  callback: flags.callback.oauth === "" ? Option.none() : Option.some(flags.callback),
  error: "",
  sheet: Option.none(),
});

const storedPair = (pair: { readonly name: string }): SheetPair => ({
  name: pair.name,
  value: "",
  stored: true,
});

export const newSheet = (): Sheet => ({
  id: Option.none(),
  name: "",
  kind: "http",
  url: "",
  command: "",
  args: "",
  headers: [],
  env: [],
  clientId: "",
  clientSecret: "",
  confirmingDelete: false,
  error: "",
  pending: "none",
});

export const sheetOf = (server: McpServer): Sheet => ({
  ...newSheet(),
  id: Option.some(server.id),
  name: server.name,
  kind: server.kind,
  url: server.url,
  command: server.command,
  args: server.args.join("\n"),
  headers: server.headers.map(storedPair),
  env: server.env.map(storedPair),
});

export const pairsOf = (sheet: Sheet) => (sheet.kind === "http" ? sheet.headers : sheet.env);

export const withPairs = (sheet: Sheet, pairs: readonly SheetPair[]): Sheet =>
  sheet.kind === "http" ? { ...sheet, headers: pairs } : { ...sheet, env: pairs };

export const toPairs = (pairs: readonly SheetPair[]): Pair[] =>
  pairs.map((pair) => ({ name: pair.name, value: pair.value }));

export const argsOf = (text: string) => text.split("\n").filter((line) => line !== "");

export const kindText = (kind: Kind) => (kind === "http" ? "HTTP" : "stdio");

export const serverNamed = (model: Model, id: number) =>
  Option.getOrUndefined(model.servers)?.find((server) => server.id === id);

// Only a saved server that is http on the server can connect.
export const canConnect = (model: Model, sheet: Sheet) =>
  sheet.kind === "http" && Option.exists(sheet.id, (id) => serverNamed(model, id)?.kind === "http");
