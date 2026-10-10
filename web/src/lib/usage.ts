import type { Usage, UsageValues } from "@/api/api.gen";

export const periods = [
  { value: "7d", title: "Last 7 days" },
  { value: "30d", title: "Last 30 days" },
  { value: "month", title: "This month" },
  { value: "custom", title: "Custom" },
] as const;

// The filters and the groups have the same five values.
export const dimensions = [
  { key: "harness", title: "Harness", options: "harnesses" },
  { key: "model", title: "Model", options: "models" },
  { key: "effort", title: "Effort", options: "efforts" },
  { key: "role", title: "Role", options: "roles" },
  { key: "repository", title: "Repository", options: "repositories" },
] as const;

export type Period = (typeof periods)[number]["value"];
export type Dimension = (typeof dimensions)[number]["key"];

// custom takes from and to as "YYYY-MM-DD" in the time zone of the browser. Both days are in the period.
export type UsageSearch = {
  tab?: "cost" | "tokens";
  period?: Period;
  from?: string;
  to?: string;
  group?: Dimension;
} & { [key in Dimension]?: string[] };

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

export function validateUsageSearch(search: Record<string, unknown>): UsageSearch {
  const from = day(search.from);
  const to = day(search.to);
  const period = periods.find((item) => item.value === search.period)?.value;
  return {
    tab: search.tab === "tokens" ? "tokens" : undefined,
    period: period === "custom" && !(from && to) ? undefined : period,
    from,
    to,
    group: dimensions.find((item) => item.key === search.group)?.key,
    harness: strings(search.harness),
    model: strings(search.model),
    effort: strings(search.effort),
    role: strings(search.role),
    repository: strings(search.repository),
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

export function tokensOf(values: UsageValues) {
  const counts = [
    values.inputTokens,
    values.outputTokens,
    values.cacheReadTokens,
    values.cacheWriteTokens,
  ].filter((tokens) => tokens !== null);
  return counts.length > 0 ? counts.reduce((sum, tokens) => sum + tokens, 0) : null;
}

export const groupKey = (index: number) => `group${index}`;

// A row has one value for each group key. A day with no value for a group has no key.
export function chartRows(
  usage: Usage,
  range: { from: Date; to: Date },
  metric: (values: UsageValues) => number | null,
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
  for (const item of usage.days) {
    const value = metric(item.values);
    const index = usage.groups.findIndex((group) => group.group === item.group);
    const row = rows.get(item.day);
    if (row && value !== null) {
      row[groupKey(index)] = value;
    }
  }
  return [...rows.values()];
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
