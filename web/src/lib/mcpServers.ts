import { Schema, SchemaGetter } from "effect";
import { lenient, type SearchRecord } from "./search";

export type McpServersSearch = {
  oauth?: "connected" | "failed";
  server?: number;
  reason?: string;
};

const ServerNumber = Schema.Json.pipe(
  Schema.decodeTo(Schema.Int.check(Schema.isGreaterThan(0)), {
    decode: SchemaGetter.transform((value) => Number(value)),
    encode: SchemaGetter.transform((server) => server),
  }),
);

const McpServersSearchFields = Schema.Struct({
  oauth: lenient(Schema.Literals(["connected", "failed"])),
  server: lenient(ServerNumber),
  reason: lenient(Schema.String),
});

const decodeMcpServersSearch = Schema.decodeUnknownSync(McpServersSearchFields);

// The callback of an MCP OAuth flow sends the browser to the page with the result in the query.
export function validateMcpServersSearch(search: SearchRecord): McpServersSearch {
  return decodeMcpServersSearch(search);
}
