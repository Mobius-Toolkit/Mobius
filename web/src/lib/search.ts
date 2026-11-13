import { Effect, Schema } from "effect";

// The value of a search param after the router reads it: JSON when the text parses as JSON, else the text.
export type SearchRecord = Record<string, typeof Schema.Json.Type>;

// A field that does not decode has no value, and the other fields stay.
export const lenient = <T, E>(field: Schema.Codec<T, E>) =>
  Schema.optional(field.pipe(Schema.catchDecoding(() => Effect.succeedNone)));
