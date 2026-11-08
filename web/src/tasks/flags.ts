import { Schema } from "effect";

export const Flags = Schema.Struct({ organization: Schema.String });

export type Flags = Schema.Schema.Type<typeof Flags>;
