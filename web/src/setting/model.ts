import { Option, Schema } from "effect";
import { Choice, HarnessChoices, IssueSetting, Target } from "./schemas";

export const Model = Schema.Struct({
  title: Schema.String,
  target: Target,
  // own is the setting that the Owner made. fallback is the binding that applies with no setting.
  own: Schema.Option(IssueSetting),
  fallback: IssueSetting,
  fallbackSource: Schema.Literals(["Workstream", "role"]),
  harnesses: Schema.Option(Schema.Array(HarnessChoices)),
  harness: Schema.String,
  model: Schema.String,
  effort: Schema.String,
  openChoice: Schema.Option(Choice),
  error: Schema.String,
  saving: Schema.Boolean,
});

export type Model = Schema.Schema.Type<typeof Model>;

export function initialModel(options: {
  title: string;
  target: Model["target"];
  own: Option.Option<IssueSetting>;
  fallback: IssueSetting;
  fallbackSource: Model["fallbackSource"];
}): Model {
  const start = Option.getOrElse(options.own, () => options.fallback);
  return {
    ...options,
    harnesses: Option.none(),
    harness: start.harness,
    model: start.model,
    effort: start.effort,
    openChoice: Option.none(),
    error: "",
    saving: false,
  };
}

export const models = (model: Model) =>
  Option.getOrElse(model.harnesses, () => []).find((other) => other.harness === model.harness)
    ?.models ?? [];

export const efforts = (model: Model) => {
  const chosen = models(model).find((other) => other.model === model.model);
  if (chosen) {
    return chosen.efforts;
  }
  return model.effort ? [model.effort] : [];
};
