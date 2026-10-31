import type { QualityValues, TimeStat, UsageValues } from "@/api/api.gen";

export const periods = [
  { value: "7d", title: "Last 7 days" },
  { value: "30d", title: "Last 30 days" },
  { value: "month", title: "This month" },
  { value: "custom", title: "Custom" },
] as const;

export const dimensions = [
  { key: "harness", title: "Harness", options: "harnesses" },
  { key: "model", title: "Model", options: "models" },
  { key: "modelVersion", title: "Model version", options: "modelVersions" },
  { key: "effort", title: "Effort", options: "efforts" },
  { key: "role", title: "Role", options: "roles" },
  { key: "repository", title: "Repository", options: "repositories" },
] as const;

export const skillDimensions = [
  { key: "harness", title: "Harness", options: "harnesses" },
  { key: "role", title: "Role", options: "roles" },
  { key: "repository", title: "Repository", options: "repositories" },
  { key: "source", title: "Source", options: "sources" },
] as const;

export const groupOptions = [
  { key: "harness", title: "Harness" },
  { key: "model", title: "Model" },
  { key: "modelVersion", title: "Model version" },
  { key: "effort", title: "Effort" },
  { key: "role", title: "Role" },
  { key: "repository", title: "Repository" },
] as const;

export const skillGroups = [
  { key: "harness", title: "Harness" },
  { key: "role", title: "Role" },
  { key: "repository", title: "Repository" },
  { key: "source", title: "Source" },
] as const;

export const qualityDimensions = [
  { key: "harness", title: "Harness", options: "harnesses" },
  { key: "model", title: "Model", options: "models" },
  { key: "effort", title: "Effort", options: "efforts" },
  { key: "repository", title: "Repository", options: "repositories" },
] as const;

export const qualityGroups = [
  { key: "harness", title: "Harness" },
  { key: "model", title: "Model" },
  { key: "effort", title: "Effort" },
  { key: "repository", title: "Repository" },
] as const;

export const qualityValues = [
  { key: "reviewRounds", title: "Review rounds" },
  { key: "fixRounds", title: "Fix rounds" },
  { key: "needsHuman", title: "Needs human" },
  { key: "questions", title: "Questions" },
  { key: "workSeconds", title: "Work time" },
  { key: "costUsd", title: "Cost" },
  { key: "tokens", title: "Tokens" },
] as const;

export type Period = (typeof periods)[number]["value"];
export type Dimension = (typeof dimensions)[number]["key"];
export type Group =
  | (typeof groupOptions)[number]["key"]
  | (typeof skillGroups)[number]["key"]
  | (typeof qualityGroups)[number]["key"];
export type SkillDimension = (typeof skillDimensions)[number]["key"];
export type QualityDimension = (typeof qualityDimensions)[number]["key"];
export type QualityValue = (typeof qualityValues)[number]["key"];

// custom takes from and to as "YYYY-MM-DD" in the time zone of the browser. Both days are in the period.
export type UsageSearch = {
  tab?: "tokens" | "times" | "skills" | "quality";
  period?: Period;
  from?: string;
  to?: string;
  group?: Group[];
  stat?: string;
  value?: QualityValue;
} & { [key in Dimension | SkillDimension]?: string[] };

// The color of a group follows its position, and the position follows the sort order of the API.
export const palette = [
  "oklch(0.623 0.214 259.8)",
  "oklch(0.705 0.213 47.6)",
  "oklch(0.723 0.219 149.6)",
  "oklch(0.627 0.265 303.9)",
  "oklch(0.656 0.241 354.3)",
  "oklch(0.715 0.143 215.2)",
  "oklch(0.795 0.184 86)",
  "oklch(0.637 0.237 25.3)",
] as const;

const parseDay = (value: string) => new Date(`${value}T00:00:00`);

function day(value: unknown) {
  return typeof value === "string" &&
    /^\d{4}-\d{2}-\d{2}$/.test(value) &&
    !Number.isNaN(parseDay(value).getTime())
    ? value
    : undefined;
}

function strings(value: unknown) {
  const list = Array.isArray(value)
    ? value.filter((item): item is string => typeof item === "string")
    : [];
  return list.length > 0 ? list : undefined;
}

function groupFields(value: unknown) {
  const fields = (strings(value) ?? []).filter((key): key is Group =>
    [...groupOptions, ...skillGroups].some((item) => item.key === key),
  );
  const unique = [...new Set(fields)];
  return unique.length > 0 ? unique : undefined;
}

export function validateUsageSearch(search: Record<string, unknown>): UsageSearch {
  const from = day(search.from);
  const to = day(search.to);
  const period = periods.find((item) => item.value === search.period)?.value;
  return {
    tab:
      search.tab === "tokens" ||
      search.tab === "times" ||
      search.tab === "skills" ||
      search.tab === "quality"
        ? search.tab
        : undefined,
    period: period === "custom" && !(from && to) ? undefined : period,
    from,
    to,
    group: groupFields(search.group),
    stat:
      typeof search.stat === "string" && /^(step|state):.+/.test(search.stat)
        ? search.stat
        : undefined,
    value: qualityValues.find((item) => item.key === search.value)?.key,
    harness: strings(search.harness),
    model: strings(search.model),
    modelVersion: strings(search.modelVersion),
    effort: strings(search.effort),
    role: strings(search.role),
    repository: strings(search.repository),
    source: strings(search.source),
  };
}

export function formatDay(date: Date) {
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const dayOfMonth = String(date.getDate()).padStart(2, "0");
  return `${date.getFullYear()}-${month}-${dayOfMonth}`;
}

// The period starts at midnight of the browser time zone. to is the start of the day after the last day.
export function usageRange(search: UsageSearch, now: Date) {
  const year = now.getFullYear();
  const month = now.getMonth();
  const today = now.getDate();
  switch (search.period) {
    case "30d":
      return { from: new Date(year, month, today - 29), to: new Date(year, month, today + 1) };
    case "month":
      return { from: new Date(year, month, 1), to: new Date(year, month + 1, 1) };
    case "custom": {
      const last = parseDay(search.to ?? "");
      return {
        from: parseDay(search.from ?? ""),
        to: new Date(last.getFullYear(), last.getMonth(), last.getDate() + 1),
      };
    }
    default:
      return { from: new Date(year, month, today - 6), to: new Date(year, month, today + 1) };
  }
}

export const costOf = (values: UsageValues) => values.costUsd;

type TokenValues = Pick<
  UsageValues,
  "inputTokens" | "outputTokens" | "cacheReadTokens" | "cacheWriteTokens"
>;

export function tokensOf(values: TokenValues) {
  const counts = [
    values.inputTokens,
    values.outputTokens,
    values.cacheReadTokens,
    values.cacheWriteTokens,
  ].filter((tokens) => tokens !== null);
  return counts.length > 0 ? counts.reduce((sum, tokens) => sum + tokens, 0) : null;
}

export const findQualityValue = (value?: string) =>
  qualityValues.find((item) => item.key === value) ?? qualityValues[0];

export function qualityMetric(values: QualityValues, key: QualityValue): number | null {
  switch (key) {
    case "reviewRounds":
      return values.reviewRounds;
    case "fixRounds":
      return values.fixRounds;
    case "needsHuman":
      return values.needsHuman;
    case "questions":
      return values.questions;
    case "workSeconds":
      return values.workSeconds;
    case "costUsd":
      return values.costUsd;
    case "tokens":
      return tokensOf(values);
  }
}

export function formatQuality(key: QualityValue, value: number | null) {
  if (value === null) {
    return "—";
  }
  switch (key) {
    case "workSeconds":
      return duration(value);
    case "costUsd":
      return cost(value);
    case "tokens":
      return compact(value);
    default:
      return value.toLocaleString([], {
        minimumFractionDigits: 1,
        maximumFractionDigits: 1,
      });
  }
}

// The count values show one decimal. The axis uses a step that one decimal shows,
// so each label agrees with its grid line. Recharts puts its ticks at quarter steps.
export function qualityAxisTicks(value: QualityValue, rows: Record<string, number | string>[]) {
  if (
    value !== "reviewRounds" &&
    value !== "fixRounds" &&
    value !== "needsHuman" &&
    value !== "questions"
  ) {
    return undefined;
  }
  let max = 0;
  for (const row of rows) {
    for (const [key, item] of Object.entries(row)) {
      if (key !== "day" && typeof item === "number" && item > max) {
        max = item;
      }
    }
  }
  if (max <= 0) {
    return [0, 1];
  }
  let step = 0;
  for (let magnitude = 0.1; step === 0; magnitude *= 10) {
    for (const factor of magnitude < 1 ? [1, 2, 5] : [1, 2, 2.5, 5]) {
      if (max / (factor * magnitude) <= 4) {
        step = factor * magnitude;
        break;
      }
    }
  }
  const last = Math.ceil(max / step);
  return Array.from({ length: last + 1 }, (_, index) => Number((index * step).toFixed(1)));
}

export function mergedShare(values: QualityValues) {
  return values.tasks === 0 ? "—" : `${Math.round((values.merged / values.tasks) * 100)}%`;
}

export const groupKey = (index: number) => `group${index}`;

const sameGroup = (a: string[], b: string[]) =>
  a.length === b.length && a.every((value, index) => value === b[index]);

// A row has one value for each group key. A day with no value for a group has no key.
export function chartRows<D extends { day: string; group: string[] }>(
  days: D[],
  groups: { group: string[] }[],
  range: { from: Date; to: Date },
  metric: (item: D) => number | null,
) {
  const rows = new Map<string, Record<string, number | string>>();
  for (
    let date = range.from;
    date < range.to;
    date = new Date(date.getFullYear(), date.getMonth(), date.getDate() + 1)
  ) {
    const key = formatDay(date);
    rows.set(key, { day: key });
  }
  for (const item of days) {
    const value = metric(item);
    const index = groups.findIndex((group) => sameGroup(group.group, item.group));
    const row = rows.get(item.day);
    if (row && value !== null) {
      row[groupKey(index)] = value;
    }
  }
  return [...rows.values()];
}

export const statValue = (stat: TimeStat) => `${stat.kind}:${stat.name}`;

export const statTitle = (stat: TimeStat) =>
  `${stat.kind === "step" ? "Step" : "State"}: ${stat.name.replaceAll("_", " ")}`;

export const findStat = (stats: TimeStat[], value?: string) =>
  stats.find((stat) => statValue(stat) === value) ?? stats[0];

// A duration shows its two largest units, for example "42s", "5m 12s", "3h 05m" and "2d 4h".
export function duration(seconds: number) {
  const total = Math.round(seconds);
  const days = Math.floor(total / 86400);
  const hours = Math.floor((total % 86400) / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const rest = total % 60;
  if (days > 0) {
    return `${days}d ${hours}h`;
  }
  if (hours > 0) {
    return `${hours}h ${String(minutes).padStart(2, "0")}m`;
  }
  if (minutes > 0) {
    return `${minutes}m ${String(rest).padStart(2, "0")}s`;
  }
  return `${rest}s`;
}

export function dayTitle(value: string) {
  return parseDay(value).toLocaleDateString([], { month: "short", day: "numeric" });
}

export function cost(value: number | null) {
  return value === null ? "—" : value.toLocaleString([], { style: "currency", currency: "USD" });
}

export function count(value: number | null) {
  return value === null ? "—" : value.toLocaleString();
}

export function compact(value: number) {
  return value.toLocaleString([], { notation: "compact" });
}

export function context(value: number | null, size: number | null) {
  return value === null || size === null ? "—" : `${compact(value)} of ${compact(size)}`;
}
