import { Schema } from "effect";

export const Page = Schema.Literals(["overview", "tools", "permissions", "labels", "workstreams"]);

export const Flags = Schema.Struct({ page: Page, organization: Schema.String });

export type Page = typeof Page.Type;
export type Flags = typeof Flags.Type;
