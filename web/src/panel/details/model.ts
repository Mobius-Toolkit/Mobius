import { Option, Schema } from "effect";
import {
  GetWorkstreamDetailsResponse,
  ListMcpServersResponse,
  WorkstreamClosureResponse,
} from "@/api/schemas.gen";
import * as Setting from "@/setting/model";
import { Role } from "@/setting/schemas";

export const WorkstreamDetails = GetWorkstreamDetailsResponse.fields.data;
export const ClosureItem = WorkstreamClosureResponse.fields.data.value;

export const McpServer = ListMcpServersResponse.fields.data.value;

export type WorkstreamDetails = typeof WorkstreamDetails.Type;
export type McpServer = typeof McpServer.Type;
export type ClosureItem = typeof ClosureItem.Type;

export const Session = Schema.Literals(["lead", "implementer", "operator"]);

export type Session = typeof Session.Type;

export const sessionTitle: Record<Session, string> = {
  lead: "Lead",
  implementer: "Implementer of the Workstream",
  operator: "Operator",
};

// The confirmation before the close of the Workstream as "won't do".
export const Closure = Schema.Struct({
  items: Schema.Option(Schema.Array(ClosureItem)),
  error: Schema.String,
  busy: Schema.Boolean,
});

export type Closure = typeof Closure.Type;

export const Sheet = Schema.Struct({ session: Session, setting: Setting.Model });

export type Sheet = typeof Sheet.Type;

// The sheet of the MCP servers of the Workstream. selected has the ids that the boxes check.
export const McpSheet = Schema.Struct({
  selected: Schema.Array(Schema.Number),
  error: Schema.String,
  saving: Schema.Boolean,
});

export type McpSheet = typeof McpSheet.Type;

export const Model = Schema.Struct({
  owner: Schema.String,
  name: Schema.String,
  number: Schema.Number,
  instance: Schema.String,
  details: Schema.Option(WorkstreamDetails),
  roles: Schema.Option(Schema.Array(Role)),
  mcpServers: Schema.Option(Schema.Array(McpServer)),
  error: Schema.String,
  evergreenBusy: Schema.Boolean,
  closure: Schema.Option(Closure),
  sheet: Schema.Option(Sheet),
  mcpSheet: Schema.Option(McpSheet),
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
    details: Option.none(),
    roles: Option.none(),
    mcpServers: Option.none(),
    error: "",
    evergreenBusy: false,
    closure: Option.none(),
    sheet: Option.none(),
    mcpSheet: Option.none(),
  };
}

export const managed = (details: WorkstreamDetails) => details.maintenance || details.skills;

export const selectedNames = (servers: readonly McpServer[], ids: readonly number[]) =>
  servers.filter((server) => ids.includes(server.id)).map((server) => server.name);
