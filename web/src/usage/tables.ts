import { cn } from "cn";
import type { Html, HtmlBuilder } from "foldkit/html";
import {
  compact,
  context,
  cost,
  count,
  duration,
  formatQuality,
  groupOptions,
  mergedShare,
  qualityGroups,
  qualityMetric,
  qualityValues,
  skillGroups,
  statTitle,
  statValue,
} from "@/lib/usage";
import { inset } from "@/ui/page";
import { groupHeader, groupTitle, sourceTitle } from "./labels";
import type { Group } from "./model";
import type { Message } from "./message";
import type {
  Quality,
  SkillUsage,
  SkillUsageRow,
  TimeStat,
  UsageTimes,
  UsageValues,
  Usage,
} from "./schemas";

type H = HtmlBuilder<Message>;

const headClass = "py-2 pr-4 text-right font-normal last:pr-0";
const leftHeadClass = "py-2 pr-4 text-left font-normal";
const rowClass = "border-b last:border-b-0";
const rowHeadClass = "py-2 pr-4 text-left font-normal whitespace-nowrap";
const numberClass = "py-2 pr-4 text-right tabular-nums";

const head = (h: H, text: string, className = headClass) =>
  h.keyed("th")(text, [h.Scope("col"), h.Class(className)], [text]);

const cell = (h: H, className: string, text: string) => h.td([h.Class(className)], [text]);

function renderTable(h: H, heads: ReadonlyArray<Html>, rows: ReadonlyArray<Html>): Html {
  return h.div(
    [h.Class(cn(inset, "overflow-x-auto"))],
    [
      h.table(
        [h.Class("w-full text-sm")],
        [
          h.thead([], [h.tr([h.Class("border-b text-muted-foreground")], heads)]),
          h.tbody([], rows),
        ],
      ),
    ],
  );
}

export function renderTotals(h: H, items: ReadonlyArray<{ title: string; value: string }>): Html {
  return h.dl(
    [h.Class(cn(inset, "grid grid-cols-2 gap-4 sm:grid-cols-3"))],
    items.map((item) =>
      h.keyed("div")(
        item.title,
        [],
        [
          h.dt([h.Class("text-sm text-muted-foreground")], [item.title]),
          h.dd([h.Class("text-lg font-semibold")], [item.value]),
        ],
      ),
    ),
  );
}

function renderValuesRow(
  h: H,
  title: string,
  values: UsageValues,
  showContext: boolean,
  key: string,
): Html {
  return h.keyed("tr")(
    key,
    [h.Class(rowClass)],
    [
      h.th([h.Scope("row"), h.Class(rowHeadClass)], [title]),
      cell(h, numberClass, String(values.sessions)),
      ...(showContext
        ? [
            cell(h, numberClass, context(values.contextMax, values.contextMaxSize)),
            cell(h, numberClass, values.contextMean === null ? "—" : compact(values.contextMean)),
          ]
        : []),
      cell(h, numberClass, count(values.inputTokens)),
      cell(h, numberClass, count(values.outputTokens)),
      cell(h, numberClass, count(values.cacheReadTokens)),
      cell(h, numberClass, count(values.cacheWriteTokens)),
      cell(h, "py-2 text-right tabular-nums", cost(values.costUsd)),
    ],
  );
}

export function renderGroupTable(
  h: H,
  usage: Usage,
  group: readonly Group[],
  showContext: boolean,
): Html {
  const heads = showContext
    ? [
        "Sessions",
        "Largest context",
        "Mean context",
        "Input",
        "Output",
        "Cache read",
        "Cache write",
        "Cost",
      ]
    : ["Sessions", "Input", "Output", "Cache read", "Cache write", "Cost"];
  return renderTable(
    h,
    [
      head(h, groupHeader(group, groupOptions), leftHeadClass),
      ...heads.map((text) => head(h, text)),
    ],
    usage.groups.map((item) =>
      renderValuesRow(
        h,
        groupTitle(item.group, group),
        item.values,
        showContext,
        JSON.stringify(item.group),
      ),
    ),
  );
}

export function renderTimeTotals(h: H, stats: readonly TimeStat[]): Html {
  return h.dl(
    [h.Class(cn(inset, "grid grid-cols-2 gap-4 sm:grid-cols-3"))],
    stats.map((stat) => {
      const failures = stat.results.filter((item) => item.result !== "success");
      return h.keyed("div")(
        statValue(stat),
        [],
        [
          h.dt([h.Class("text-sm text-muted-foreground")], [statTitle(stat)]),
          h.dd([h.Class("text-lg font-semibold")], [duration(stat.totalSeconds)]),
          h.dd(
            [h.Class("text-sm text-muted-foreground")],
            [`${stat.count} × · mean ${duration(stat.meanSeconds)}`],
          ),
          failures.length > 0
            ? h.dd(
                [h.Class("text-sm text-muted-foreground")],
                [failures.map((item) => `${item.result} ${item.count}`).join(" · ")],
              )
            : h.empty,
        ],
      );
    }),
  );
}

export function renderTimeTable(h: H, times: UsageTimes, group: readonly Group[]): Html {
  const heads = ["Stat", "Count", "Total", "Mean"];
  return renderTable(
    h,
    [
      head(h, groupHeader(group, groupOptions), leftHeadClass),
      ...heads.map((text, index) =>
        head(
          h,
          text,
          cn("py-2 pr-4 font-normal last:pr-0", index === 0 ? "text-left" : "text-right"),
        ),
      ),
    ],
    times.groups.flatMap((item) =>
      item.stats.map((stat) =>
        h.keyed("tr")(
          `${JSON.stringify(item.group)}|${statValue(stat)}`,
          [h.Class(rowClass)],
          [
            h.th([h.Scope("row"), h.Class(rowHeadClass)], [groupTitle(item.group, group)]),
            cell(h, "py-2 pr-4 whitespace-nowrap", statTitle(stat)),
            cell(h, numberClass, String(stat.count)),
            cell(
              h,
              "py-2 pr-4 text-right whitespace-nowrap tabular-nums",
              duration(stat.totalSeconds),
            ),
            cell(h, "py-2 text-right whitespace-nowrap tabular-nums", duration(stat.meanSeconds)),
          ],
        ),
      ),
    ),
  );
}

export function renderQualityTable(h: H, quality: Quality, group: readonly Group[]): Html {
  const heads = [
    "Tasks",
    "Merged",
    "Review rounds",
    "Fix rounds",
    "Needs human",
    "Questions",
    "Work time",
    "Cost",
    "Tokens",
  ];
  return renderTable(
    h,
    [
      head(h, groupHeader(group, qualityGroups), leftHeadClass),
      ...heads.map((text) => head(h, text)),
    ],
    quality.groups.map((item) =>
      h.keyed("tr")(
        JSON.stringify(item.group),
        [h.Class(rowClass)],
        [
          h.th([h.Scope("row"), h.Class(rowHeadClass)], [groupTitle(item.group, group)]),
          cell(h, numberClass, String(item.values.tasks)),
          cell(h, numberClass, mergedShare(item.values)),
          ...qualityValues.map(({ key }) =>
            h.keyed("td")(
              key,
              [h.Class("py-2 pr-4 text-right whitespace-nowrap tabular-nums last:pr-0")],
              [formatQuality(key, qualityMetric(item.values, key))],
            ),
          ),
        ],
      ),
    ),
  );
}

function renderSkillRow(h: H, row: SkillUsageRow, group: readonly Group[]): Html {
  return h.keyed("tr")(
    JSON.stringify([row.skill, row.source, row.group]),
    [h.Class(rowClass)],
    [
      h.th([h.Scope("row"), h.Class(rowHeadClass)], [row.skill]),
      cell(h, "py-2 pr-4 whitespace-nowrap", sourceTitle(row.source)),
      ...(group.length > 0
        ? [cell(h, "py-2 pr-4 whitespace-nowrap", groupTitle(row.group, group))]
        : []),
      cell(h, numberClass, String(row.uses)),
      cell(h, numberClass, row.source === "harness" ? "—" : String(row.had)),
      cell(h, "py-2 text-right tabular-nums", String(row.used)),
    ],
  );
}

export function renderSkillTable(h: H, skills: SkillUsage, group: readonly Group[]): Html {
  const left = ["Skill", "Source", ...(group.length > 0 ? [groupHeader(group, skillGroups)] : [])];
  const heads = ["Uses", "Had", "Used"];
  return renderTable(
    h,
    [...left.map((text) => head(h, text, leftHeadClass)), ...heads.map((text) => head(h, text))],
    [
      ...(skills.rows.length === 0
        ? [
            h.keyed("tr")(
              "empty",
              [],
              [
                h.td(
                  [h.Colspan(left.length + heads.length), h.Class("py-2 text-muted-foreground")],
                  ["No skill uses in this period."],
                ),
              ],
            ),
          ]
        : []),
      ...skills.rows.map((row) => renderSkillRow(h, row, group)),
    ],
  );
}
