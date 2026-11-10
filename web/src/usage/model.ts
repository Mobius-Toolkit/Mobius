import { Option, Schema } from "effect";
import {
  dimensions,
  groupOptions,
  qualityDimensions,
  qualityGroups,
  skillDimensions,
  skillGroups,
  usageRange,
} from "@/lib/usage";
import { Quality, SkillUsage, Usage, UsageTimes } from "./schemas";

export const Tab = Schema.Literals(["cost", "tokens", "times", "skills", "quality"]);

export const Period = Schema.Literals(["7d", "30d", "month", "custom"]);

export const Group = Schema.Literals([
  "harness",
  "model",
  "modelVersion",
  "effort",
  "role",
  "repository",
  "source",
]);

export const QualityValue = Schema.Literals([
  "reviewRounds",
  "fixRounds",
  "needsHuman",
  "questions",
  "workSeconds",
  "costUsd",
  "tokens",
]);

const Strings = Schema.optional(Schema.Array(Schema.String));

// The search of the page. The tab Cost is the page with no tab.
export const Search = Schema.Struct({
  tab: Schema.optional(Schema.Literals(["tokens", "times", "skills", "quality"])),
  period: Schema.optional(Period),
  from: Schema.optional(Schema.String),
  to: Schema.optional(Schema.String),
  group: Schema.optional(Schema.Array(Group)),
  stat: Schema.optional(Schema.String),
  value: Schema.optional(QualityValue),
  harness: Strings,
  model: Strings,
  modelVersion: Strings,
  effort: Strings,
  role: Strings,
  repository: Strings,
  source: Strings,
});

// now is the time of the start of the page in milliseconds. The periods count from it.
export const Flags = Schema.Struct({
  search: Search,
  now: Schema.Number,
  timeZone: Schema.String,
});

// The menu of the filter of a field, or the menu of the period, the group or the chart. A field has the name of a group.
export const MenuKey = Schema.Literals(["period", "group", "chart", ...Group.literals]);

// The range of a day list is a pair of times in milliseconds. to is the start of the day after the last day.
export const Range = Schema.Struct({ from: Schema.Number, to: Schema.Number });

export const ChartSize = Schema.Struct({ width: Schema.Number, height: Schema.Number });

export const ChartPoint = Schema.Struct({ x: Schema.Number, y: Schema.Number });

// request counts the loads. A load that is not the last one has an old search, so the program drops its answer.
export const Model = Schema.Struct({
  search: Search,
  now: Schema.Number,
  timeZone: Schema.String,
  request: Schema.Number,
  openMenu: Schema.Option(MenuKey),
  groupSheetOpen: Schema.Boolean,
  usage: Schema.Option(Schema.Struct({ usage: Usage, range: Range })),
  times: Schema.Option(Schema.Struct({ times: UsageTimes, range: Range })),
  skills: Schema.Option(SkillUsage),
  quality: Schema.Option(Schema.Struct({ quality: Quality, range: Range })),
  error: Schema.String,
  chart: ChartSize,
  hover: Schema.Option(ChartPoint),
});

export type Tab = typeof Tab.Type;
export type Period = typeof Period.Type;
export type Group = typeof Group.Type;
export type QualityValue = typeof QualityValue.Type;
export type Search = typeof Search.Type;
export type Flags = typeof Flags.Type;
export type MenuKey = typeof MenuKey.Type;
export type Range = typeof Range.Type;
export type Model = typeof Model.Type;

export const initialModel = (flags: Flags): Model => ({
  ...flags,
  request: 0,
  openMenu: Option.none(),
  groupSheetOpen: false,
  usage: Option.none(),
  times: Option.none(),
  skills: Option.none(),
  quality: Option.none(),
  error: "",
  chart: { width: 0, height: 0 },
  hover: Option.none(),
});

export const tabOf = (search: Search): Tab => search.tab ?? "cost";

export const groupsOf = (tab: Tab) =>
  tab === "quality" ? qualityGroups : tab === "skills" ? skillGroups : groupOptions;

export const filtersOf = (tab: Tab) =>
  tab === "quality" ? qualityDimensions : tab === "skills" ? skillDimensions : dimensions;

// The group of the page has the fields of the tab, in the order of the tab.
export const groupOf = (search: Search, tab: Tab): Group[] =>
  groupsOf(tab)
    .map((item) => item.key)
    .filter((key) => search.group?.includes(key));

// The API gets only the fields that its route has.
export const groupFieldsOf = <Key extends Group>(
  search: Search,
  fields: readonly { key: Key }[],
): Key[] => fields.map((item) => item.key).filter((key) => search.group?.includes(key));

export const rangeOf = (model: Model): Range => {
  const range = usageRange(model.search, new Date(model.now));
  return { from: range.from.getTime(), to: range.to.getTime() };
};

type FilterOptions = { readonly [key in FilterOptionName]?: ReadonlyArray<string> };

export type FilterOptionName =
  | "harnesses"
  | "models"
  | "modelVersions"
  | "efforts"
  | "roles"
  | "repositories"
  | "sources";

// The values that a filter of the tab offers come from the last load of the tab.
export function filterOptionsOf(model: Model): FilterOptions | undefined {
  switch (tabOf(model.search)) {
    case "quality":
      return Option.getOrUndefined(model.quality)?.quality.options;
    case "skills":
      return Option.getOrUndefined(model.skills)?.options;
    case "times":
      return Option.getOrUndefined(model.times)?.times.options;
    case "cost":
    case "tokens":
      return Option.getOrUndefined(model.usage)?.usage.options;
  }
}
