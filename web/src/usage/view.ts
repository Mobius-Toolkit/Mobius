import { cn } from "cn";
import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import {
  cost,
  count,
  findQualityValue,
  findStat,
  formatQuality,
  mergedShare,
  periods,
  qualityValues,
  statTitle,
  statValue,
} from "@/lib/usage";
import { renderBadge } from "@/ui/badge";
import { buttonClass } from "@/ui/button";
import { chevronDownIcon } from "@/ui/icons";
import { inputClass } from "@/ui/input";
import { renderMenu, renderMenuItem } from "@/ui/menu";
import { inset, renderErrorBadge, renderPageHeader } from "@/ui/page";
import { defineInPlaceMount, renderSheet } from "@/ui/sheet";
import { renderTabs } from "@/ui/tabs";
import { renderQualityChart, renderTimesChart, renderUsageChart } from "./charts";
import { Message } from "./message";
import {
  filterOptionsOf,
  filtersOf,
  groupOf,
  groupsOf,
  tabOf,
  type FilterOptionName,
  type Group,
  type MenuKey,
  type Model,
  type Range,
  type Tab,
} from "./model";
import type { Quality, SkillUsage, Usage, UsageTimes } from "./schemas";
import {
  renderGroupTable,
  renderQualityTable,
  renderSkillTable,
  renderTimeTable,
  renderTimeTotals,
  renderTotals,
} from "./tables";

type H = HtmlBuilder<Message>;

const SheetOpened = defineInPlaceMount(Message.CompletedFocusSheet);

const tabs: ReadonlyArray<{ key: Tab; title: string }> = [
  { key: "cost", title: "Cost" },
  { key: "tokens", title: "Tokens" },
  { key: "times", title: "Times" },
  { key: "skills", title: "Skills" },
  { key: "quality", title: "Quality" },
];

const menuProps = (model: Model, menu: MenuKey) => ({
  id: `usage-menu-${menu}`,
  open: Option.exists(model.openMenu, (open) => open === menu),
  toggle: Message.ToggledMenu({ menu }),
  close: Message.ClosedMenu(),
});

function renderPeriodMenu(h: H, model: Model): Html {
  const period = model.search.period ?? "7d";
  return renderMenu(h, {
    ...menuProps(model, "period"),
    trigger: [periods.find((item) => item.value === period)?.title ?? "", chevronDownIcon(h)],
    items: periods.map((item) =>
      renderMenuItem(h, {
        key: item.value,
        role: "menuitemradio",
        checked: item.value === period,
        title: item.title,
        onClick: Message.SelectedPeriod({ period: item.value }),
      }),
    ),
  });
}

function renderFilterMenu(
  h: H,
  model: Model,
  item: { key: Group; title: string; options: FilterOptionName },
): Html {
  const selected = model.search[item.key] ?? [];
  const options = filterOptionsOf(model)?.[item.options] ?? [];
  const shown = [...new Set([...options, ...selected])];
  return renderMenu(h, {
    ...menuProps(model, item.key),
    disabled: shown.length === 0,
    trigger: [
      item.title,
      selected.length > 0 ? renderBadge(h, "secondary", String(selected.length)) : h.empty,
      chevronDownIcon(h),
    ],
    items: shown.map((option) =>
      renderMenuItem(h, {
        key: option,
        role: "menuitemcheckbox",
        checked: selected.includes(option),
        title: option,
        onClick: Message.ToggledFilter({ key: item.key, option }),
      }),
    ),
  });
}

function groupTriggerTitle(
  group: readonly Group[],
  groups: readonly { key: Group; title: string }[],
) {
  const titles = groups.filter((item) => group.includes(item.key));
  return `Group by: ${titles.map((item) => item.title).join(" · ") || "None"}`;
}

function renderGroupMenu(
  h: H,
  model: Model,
  group: readonly Group[],
  groups: readonly { key: Group; title: string }[],
): Html {
  return renderMenu(h, {
    ...menuProps(model, "group"),
    className: "hidden md:flex",
    trigger: [groupTriggerTitle(group, groups), chevronDownIcon(h)],
    items: groups.map((item) =>
      renderMenuItem(h, {
        key: item.key,
        role: "menuitemcheckbox",
        checked: group.includes(item.key),
        title: item.title,
        onClick: Message.ToggledGroup({ key: item.key }),
      }),
    ),
  });
}

function renderGroupSheetButton(
  h: H,
  group: readonly Group[],
  groups: readonly { key: Group; title: string }[],
): Html {
  return h.button(
    [
      h.Type("button"),
      h.Class(buttonClass({ variant: "outline", className: "md:hidden" })),
      h.OnClick(Message.OpenedGroupSheet()),
    ],
    [groupTriggerTitle(group, groups), chevronDownIcon(h)],
  );
}

function renderGroupSheet(
  h: H,
  group: readonly Group[],
  groups: readonly { key: Group; title: string }[],
): Html {
  return renderSheet(h, {
    mount: h.OnMount(SheetOpened()),
    titleId: "usage-group-title",
    title: "Group by",
    close: Message.ClosedGroupSheet(),
    className: "gap-2 p-4 pt-3",
    children: [
      h.div(
        [h.Class("flex flex-col")],
        groups.map((item) =>
          h.keyed("label")(
            item.key,
            [h.Class("flex items-center gap-2 rounded-md px-2 py-2 hover:bg-accent")],
            [
              h.input([
                h.Type("checkbox"),
                h.Class("size-4"),
                h.Checked(group.includes(item.key)),
                h.OnClick(Message.ToggledGroup({ key: item.key })),
              ]),
              item.title,
            ],
          ),
        ),
      ),
    ],
  });
}

function renderFilters(
  h: H,
  model: Model,
  tab: Tab,
  group: readonly Group[],
  groups: readonly { key: Group; title: string }[],
): Html {
  const search = model.search;
  return h.div(
    [h.Class(cn(inset, "grid gap-2"))],
    [
      h.div(
        [h.Class("flex flex-wrap gap-2")],
        [
          renderPeriodMenu(h, model),
          ...filtersOf(tab).map((item) => renderFilterMenu(h, model, item)),
          renderGroupMenu(h, model, group, groups),
          renderGroupSheetButton(h, group, groups),
        ],
      ),
      search.period === "custom"
        ? h.div(
            [h.Class("flex flex-wrap items-center gap-2")],
            [
              h.input([
                h.Type("date"),
                h.AriaLabel("From"),
                h.Class(inputClass("w-auto")),
                h.Value(search.from ?? ""),
                ...(search.to === undefined ? [] : [h.Max(search.to)]),
                h.OnInput((value) => Message.ChangedFrom({ value })),
              ]),
              h.input([
                h.Type("date"),
                h.AriaLabel("To"),
                h.Class(inputClass("w-auto")),
                h.Value(search.to ?? ""),
                ...(search.from === undefined ? [] : [h.Min(search.from)]),
                h.OnInput((value) => Message.ChangedTo({ value })),
              ]),
            ],
          )
        : h.empty,
    ],
  );
}

function renderUsageTab(
  h: H,
  model: Model,
  usage: Usage,
  range: Range,
  group: readonly Group[],
  kind: "cost" | "tokens",
): Html {
  const totals = usage.totals;
  return h.div(
    [h.Class("grid gap-6")],
    [
      renderTotals(
        h,
        kind === "cost"
          ? [
              { title: "Cost", value: cost(totals.costUsd) },
              { title: "Sessions", value: String(totals.sessions) },
            ]
          : [
              { title: "Input", value: count(totals.inputTokens) },
              { title: "Output", value: count(totals.outputTokens) },
              { title: "Cache read", value: count(totals.cacheReadTokens) },
              { title: "Cache write", value: count(totals.cacheWriteTokens) },
              { title: "Sessions", value: String(totals.sessions) },
            ],
      ),
      renderUsageChart(h, model, usage, range, group, kind),
      renderGroupTable(h, usage, group, kind === "tokens"),
    ],
  );
}

function renderTimesTab(
  h: H,
  model: Model,
  times: UsageTimes,
  range: Range,
  group: readonly Group[],
): Html {
  const selected = findStat(times.totals, model.search.stat);
  return h.div(
    [h.Class("grid gap-6")],
    [
      renderTimeTotals(h, times.totals),
      h.div(
        [h.Class("grid gap-2")],
        [
          h.div(
            [h.Class(inset)],
            [
              renderMenu(h, {
                ...menuProps(model, "chart"),
                disabled: selected === undefined,
                trigger: [
                  `Chart: ${selected === undefined ? "" : statTitle(selected)}`,
                  chevronDownIcon(h),
                ],
                items: times.totals.map((stat) =>
                  renderMenuItem(h, {
                    key: statValue(stat),
                    role: "menuitemradio",
                    checked: selected !== undefined && statValue(stat) === statValue(selected),
                    title: statTitle(stat),
                    onClick: Message.SelectedStat({ stat: statValue(stat) }),
                  }),
                ),
              }),
            ],
          ),
          renderTimesChart(h, model, times, range, group),
        ],
      ),
      renderTimeTable(h, times, group),
    ],
  );
}

function renderQualityTab(
  h: H,
  model: Model,
  quality: Quality,
  range: Range,
  group: readonly Group[],
): Html {
  const selected = findQualityValue(model.search.value);
  const totals = quality.totals;
  return h.div(
    [h.Class("grid gap-6")],
    [
      renderTotals(h, [
        { title: "Tasks", value: String(totals.tasks) },
        { title: "Merged", value: mergedShare(totals) },
        { title: "Review rounds", value: formatQuality("reviewRounds", totals.reviewRounds) },
        { title: "Fix rounds", value: formatQuality("fixRounds", totals.fixRounds) },
        { title: "Cost per task", value: cost(totals.costUsd) },
      ]),
      h.div(
        [h.Class("grid gap-2")],
        [
          h.div(
            [h.Class(inset)],
            [
              renderMenu(h, {
                ...menuProps(model, "chart"),
                trigger: [`Chart: ${selected.title}`, chevronDownIcon(h)],
                items: qualityValues.map((item) =>
                  renderMenuItem(h, {
                    key: item.key,
                    role: "menuitemradio",
                    checked: item.key === selected.key,
                    title: item.title,
                    onClick: Message.SelectedValue({ value: item.key }),
                  }),
                ),
              }),
            ],
          ),
          renderQualityChart(h, model, quality, range, group, selected.key),
        ],
      ),
      renderQualityTable(h, quality, group),
    ],
  );
}

function renderSkillsTab(h: H, skills: SkillUsage, group: readonly Group[]): Html {
  return h.div(
    [h.Class("grid gap-6")],
    [
      renderTotals(h, [
        { title: "Uses", value: String(skills.totals.uses) },
        { title: "Skills", value: String(skills.totals.skills) },
        { title: "Sessions", value: String(skills.totals.sessions) },
      ]),
      renderSkillTable(h, skills, group),
    ],
  );
}

function renderPanel(h: H, model: Model, tab: Tab, group: readonly Group[]): ReadonlyArray<Html> {
  switch (tab) {
    case "cost":
    case "tokens":
      return Option.match(model.usage, {
        onNone: () => [],
        onSome: ({ usage, range }) => [renderUsageTab(h, model, usage, range, group, tab)],
      });
    case "times":
      return Option.match(model.times, {
        onNone: () => [],
        onSome: ({ times, range }) => [renderTimesTab(h, model, times, range, group)],
      });
    case "skills":
      return Option.match(model.skills, {
        onNone: () => [],
        onSome: (skills) => [renderSkillsTab(h, skills, group)],
      });
    case "quality":
      return Option.match(model.quality, {
        onNone: () => [],
        onSome: ({ quality, range }) => [renderQualityTab(h, model, quality, range, group)],
      });
  }
}

export function view(model: Model, h: H): Html {
  const tab = tabOf(model.search);
  const groups = groupsOf(tab);
  const group = groupOf(model.search, tab);
  return h.div(
    [h.Class("grid gap-6")],
    [
      renderPageHeader(h, "Usage"),
      renderFilters(h, model, tab, group, groups),
      model.error ? renderErrorBadge(h, model.error) : h.empty,
      renderTabs(h, {
        id: "usage",
        listClassName: "mx-4 md:mx-0",
        tabs: tabs.map((item) => ({ ...item, select: Message.SelectedTab({ tab: item.key }) })),
        selected: tab,
        panel: renderPanel(h, model, tab, group),
      }),
      model.groupSheetOpen ? renderGroupSheet(h, group, groups) : h.empty,
    ],
  );
}
