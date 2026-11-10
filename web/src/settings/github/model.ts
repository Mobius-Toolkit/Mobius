import { Option, Schema } from "effect";
import { GitHubApp } from "../schemas";

// setup is true for the first screen of a server that has no App. It has no page header and no list of Apps.
export const Flags = Schema.Struct({ setup: Schema.Boolean });

export const Model = Schema.Struct({
  setup: Schema.Boolean,
  apps: Schema.Array(GitHubApp),
  account: Schema.String,
  typedName: Schema.Option(Schema.String),
  error: Schema.Option(Schema.String),
  busy: Schema.Boolean,
});

export type Flags = Schema.Schema.Type<typeof Flags>;
export type Model = Schema.Schema.Type<typeof Model>;

export const initialModel = (flags: Flags): Model => ({
  setup: flags.setup,
  apps: [],
  account: "",
  typedName: Option.none(),
  error: Option.none(),
  busy: false,
});

// The name follows the account until the Owner edits it. GitHub accepts at most 34 characters.
export const nameOf = (model: Model) =>
  Option.getOrElse(model.typedName, () => `Mobius ${model.account}`.slice(0, 34).trimEnd());
