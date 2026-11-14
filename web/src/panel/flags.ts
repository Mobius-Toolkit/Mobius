import { Schema } from "effect";
import { Opening, Tab } from "./schemas";

// instance names the place of the panel, because the page can hold two panels, and their ids must differ.
export const Flags = Schema.Struct({
  owner: Schema.String,
  name: Schema.String,
  number: Schema.Number,
  instance: Schema.String,
  tab: Tab,
  opening: Schema.Option(Opening),
});

export type Flags = Schema.Schema.Type<typeof Flags>;
