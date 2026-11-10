import { Schema } from "effect";
import { ListMcpServersResponse } from "@/api/schemas.gen";

export const McpServer = ListMcpServersResponse.fields.data.value;

export const Kind = Schema.Literals(["http", "stdio"]);

export const Pair = Schema.Struct({ name: Schema.String, value: Schema.String });

export const Body = Schema.Struct({
  name: Schema.String,
  kind: Kind,
  url: Schema.String,
  command: Schema.String,
  args: Schema.Array(Schema.String),
  headers: Schema.Array(Pair),
  env: Schema.Array(Pair),
});

// The query of the page where the authorization server sends the browser back. oauth is empty on any other visit, and
// server is 0 then.
export const Callback = Schema.Struct({
  oauth: Schema.Literals(["", "connected", "failed"]),
  server: Schema.Number,
  reason: Schema.String,
});

export type McpServer = typeof McpServer.Type;
export type Kind = typeof Kind.Type;
export type Pair = typeof Pair.Type;
export type Body = typeof Body.Type;
export type Callback = typeof Callback.Type;
