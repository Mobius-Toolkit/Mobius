import { cn } from "cn";
import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import {
  chartRows,
  compact,
  cost,
  costOf,
  count,
  duration,
  findStat,
  formatQuality,
  palette,
  qualityAxisTicks,
  qualityMetric,
  statValue,
  tokensOf,
} from "@/lib/usage";
import { defineChartMount, renderBarChart, renderLineChart, type ChartSeries } from "@/ui/chart";
import { inset } from "@/ui/page";
import { groupTitle } from "./labels";
import { Message } from "./message";
import type { Group, Model, QualityValue, Range } from "./model";
import type { Quality, Usage, UsageTimes } from "./schemas";

type H = HtmlBuilder<Message>;

const ChartBox = defineChartMount(Message.ResizedChart, Message.HoveredChart, Message.LeftChart);

const chartClass = cn(inset, "aspect-auto h-72 w-full");

const daysOf = (range: Range) => ({ from: new Date(range.from), to: new Date(range.to) });

const seriesOf = (
  groups: readonly { group: readonly string[] }[],
  group: readonly Group[],
): ChartSeries[] =>
  groups.map((item, index) => ({
    label: groupTitle(item.group, group),
    color: palette[index % palette.length],
  }));

export function renderUsageChart(
  h: H,
  model: Model,
  usage: Usage,
  range: Range,
  group: readonly Group[],
  kind: "cost" | "tokens",
): Html {
  const metric = kind === "cost" ? costOf : tokensOf;
  return renderBarChart(h, {
    mount: h.OnMount(ChartBox()),
    className: chartClass,
    size: model.chart,
    hover: Option.getOrUndefined(model.hover),
    rows: chartRows(usage.days, usage.groups, daysOf(range), (item) => metric(item.values)),
    series: seriesOf(usage.groups, group),
    legend: group.length > 0,
    axisFormat: kind === "cost" ? cost : compact,
    valueFormat: kind === "cost" ? cost : count,
  });
}

export function renderTimesChart(
  h: H,
  model: Model,
  times: UsageTimes,
  range: Range,
  group: readonly Group[],
): Html {
  const selected = findStat(times.totals, model.search.stat);
  return renderBarChart(h, {
    mount: h.OnMount(ChartBox()),
    className: chartClass,
    size: model.chart,
    hover: Option.getOrUndefined(model.hover),
    rows: chartRows(times.days, times.groups, daysOf(range), (item) => {
      const found = item.stats.find(
        (candidate) => selected && statValue(candidate) === statValue(selected),
      );
      return found ? found.totalSeconds : null;
    }),
    series: seriesOf(times.groups, group),
    legend: group.length > 0,
    axisFormat: duration,
    valueFormat: duration,
  });
}

export function renderQualityChart(
  h: H,
  model: Model,
  quality: Quality,
  range: Range,
  group: readonly Group[],
  value: QualityValue,
): Html {
  const rows = chartRows(quality.days, quality.groups, daysOf(range), (item) =>
    qualityMetric(item.values, value),
  );
  return renderLineChart(h, {
    mount: h.OnMount(ChartBox()),
    className: chartClass,
    size: model.chart,
    hover: Option.getOrUndefined(model.hover),
    rows,
    series: seriesOf(quality.groups, group),
    legend: group.length > 0,
    axisFormat: (tick) => formatQuality(value, tick),
    valueFormat: (item) => formatQuality(value, item),
    ticks: qualityAxisTicks(value, rows),
  });
}
