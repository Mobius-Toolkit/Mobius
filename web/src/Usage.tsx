import { ChevronDownIcon } from "lucide-react";
import { use, useEffect, useMemo, useState } from "react";
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts";
import {
  getUsage,
  getUsageSkills,
  getUsageTimes,
  type GetUsageParams,
  type GetUsageSkillsParams,
  type SkillUsage,
  type SkillUsageRow,
  type TimeStat,
  type Usage as UsageView,
  type UsageTimes,
  type UsageValues,
} from "@/api/api.gen";
import { ErrorBadge, inset, PageHeader } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  ChartContainer,
  ChartLegend,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { LoginContext } from "@/lib/login";
import {
  chartRows,
  compact,
  cost,
  costOf,
  count,
  dayTitle,
  dimensions,
  duration,
  findStat,
  formatDay,
  groupKey,
  groupOptions,
  palette,
  periods,
  skillDimensions,
  skillGroups,
  statTitle,
  statValue,
  tokensOf,
  usageRange,
  type Dimension,
  type Group,
  type Period,
  type SkillDimension,
  type UsageSearch,
} from "@/lib/usage";
import { cn } from "@/lib/utils";
import { TopBar } from "./TopBar";

type Range = ReturnType<typeof usageRange>;

function PeriodMenu({
  search,
  onSearch,
}: {
  search: UsageSearch;
  onSearch: (patch: UsageSearch) => void;
}) {
  const period = search.period ?? "7d";
  const select = (value: string) => {
    const next = value as Period;
    if (next === "custom") {
      const range = usageRange({ period: "30d" }, new Date());
      const last = new Date(range.to.getFullYear(), range.to.getMonth(), range.to.getDate() - 1);
      onSearch({ period: next, from: formatDay(range.from), to: formatDay(last) });
    } else {
      onSearch({ period: next, from: undefined, to: undefined });
    }
  };
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline">
          {periods.find((item) => item.value === period)?.title}
          <ChevronDownIcon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start">
        <DropdownMenuRadioGroup value={period} onValueChange={select}>
          {periods.map((item) => (
            <DropdownMenuRadioItem key={item.value} value={item.value}>
              {item.title}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function FilterMenu({
  title,
  options,
  selected,
  onChange,
}: {
  title: string;
  options: string[];
  selected: string[];
  onChange: (selected: string[]) => void;
}) {
  const shown = [...new Set([...options, ...selected])];
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" disabled={shown.length === 0}>
          {title}
          {selected.length > 0 && <Badge variant="secondary">{selected.length}</Badge>}
          <ChevronDownIcon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start">
        {shown.map((option) => (
          <DropdownMenuCheckboxItem
            key={option}
            checked={selected.includes(option)}
            onSelect={(event) => event.preventDefault()}
            onCheckedChange={(checked) =>
              onChange(
                checked ? [...selected, option] : selected.filter((value) => value !== option),
              )
            }
          >
            {option}
          </DropdownMenuCheckboxItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// The API and the URL keep the fields in the order of the tab, not the order of the selection.
function toggleGroup(
  group: Group[],
  fields: readonly { key: Group }[],
  key: Group,
  checked: boolean,
) {
  return fields
    .map((item) => item.key)
    .filter((other) => (other === key ? checked : group.includes(other)));
}

function groupTriggerTitle(group: Group[], groups: readonly { key: Group; title: string }[]) {
  const titles = groups.filter((item) => group.includes(item.key));
  return `Group by: ${titles.map((item) => item.title).join(" · ") || "None"}`;
}

function GroupMenu({
  group,
  groups,
  onChange,
}: {
  group: Group[];
  groups: readonly { key: Group; title: string }[];
  onChange: (group: Group[]) => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" className="hidden md:inline-flex">
          {groupTriggerTitle(group, groups)}
          <ChevronDownIcon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start">
        {groups.map((item) => (
          <DropdownMenuCheckboxItem
            key={item.key}
            checked={group.includes(item.key)}
            onSelect={(event) => event.preventDefault()}
            onCheckedChange={(checked) =>
              onChange(toggleGroup(group, groups, item.key, checked === true))
            }
          >
            {item.title}
          </DropdownMenuCheckboxItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function GroupSheet({
  group,
  groups,
  onChange,
}: {
  group: Group[];
  groups: readonly { key: Group; title: string }[];
  onChange: (group: Group[]) => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Button variant="outline" className="md:hidden">
          {groupTriggerTitle(group, groups)}
          <ChevronDownIcon />
        </Button>
      </SheetTrigger>
      <SheetContent side="bottom" aria-describedby={undefined} className="gap-2 p-4 pt-3">
        <SheetTitle>Group by</SheetTitle>
        <div className="flex flex-col">
          {groups.map((item) => (
            <label
              key={item.key}
              className="flex items-center gap-2 rounded-md px-2 py-2 hover:bg-accent"
            >
              <input
                type="checkbox"
                className="size-4"
                checked={group.includes(item.key)}
                onChange={(event) =>
                  onChange(toggleGroup(group, groups, item.key, event.target.checked))
                }
              />
              {item.title}
            </label>
          ))}
        </div>
      </SheetContent>
    </Sheet>
  );
}

type FilterOptions = {
  [key in (typeof dimensions | typeof skillDimensions)[number]["options"]]?: string[];
};

function Filters({
  search,
  items,
  groups,
  group,
  options,
  onSearch,
}: {
  search: UsageSearch;
  items: readonly {
    key: Dimension | SkillDimension;
    title: string;
    options: keyof FilterOptions;
  }[];
  groups: readonly { key: Group; title: string }[];
  group: Group[];
  options?: FilterOptions;
  onSearch: (patch: UsageSearch) => void;
}) {
  return (
    <div className={cn(inset, "grid gap-2")}>
      <div className="flex flex-wrap gap-2">
        <PeriodMenu search={search} onSearch={onSearch} />
        {items.map((item) => (
          <FilterMenu
            key={item.key}
            title={item.title}
            options={options?.[item.options] ?? []}
            selected={search[item.key] ?? []}
            onChange={(selected) =>
              onSearch({ [item.key]: selected.length > 0 ? selected : undefined })
            }
          />
        ))}
        <GroupMenu
          group={group}
          groups={groups}
          onChange={(next) => onSearch({ group: next.length > 0 ? next : undefined })}
        />
        <GroupSheet
          group={group}
          groups={groups}
          onChange={(next) => onSearch({ group: next.length > 0 ? next : undefined })}
        />
      </div>
      {search.period === "custom" && (
        <div className="flex flex-wrap items-center gap-2">
          <Input
            type="date"
            aria-label="From"
            className="w-auto"
            value={search.from}
            max={search.to}
            onChange={(event) => event.target.value && onSearch({ from: event.target.value })}
          />
          <Input
            type="date"
            aria-label="To"
            className="w-auto"
            value={search.to}
            min={search.from}
            onChange={(event) => event.target.value && onSearch({ to: event.target.value })}
          />
        </div>
      )}
    </div>
  );
}

function Totals({ items }: { items: { title: string; value: string }[] }) {
  return (
    <dl className={cn(inset, "grid grid-cols-2 gap-4 sm:grid-cols-3")}>
      {items.map((item) => (
        <div key={item.title}>
          <dt className="text-sm text-muted-foreground">{item.title}</dt>
          <dd className="text-lg font-semibold">{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}

const sourceTitle = (source: string) => (source === "mobius" ? "Mobius" : "Harness");

function fieldTitle(field: Group, value: string) {
  if (value === "") {
    return "—";
  }
  return field === "source" ? sourceTitle(value) : value;
}

// A row has one value for each selected field, in the order of the fields of the tab.
function groupTitle(group: string[], fields: Group[]) {
  if (fields.length === 0) {
    return "All";
  }
  return group.map((value, index) => fieldTitle(fields[index], value)).join(" · ");
}

function groupHeader(fields: Group[], groups: readonly { key: Group; title: string }[]) {
  if (fields.length === 0) {
    return "Group";
  }
  return fields.map((key) => groups.find((item) => item.key === key)?.title ?? key).join(" · ");
}

function rowFormatter(config: ChartConfig, format: (value: number | null) => string) {
  return (value: unknown, name: unknown, item: { color?: string }) => (
    <>
      <span className="size-2.5 shrink-0 rounded-[2px]" style={{ background: item.color }} />
      <span className="grow text-muted-foreground">{config[String(name)]?.label}</span>
      <span className="font-mono font-medium tabular-nums">{format(Number(value))}</span>
    </>
  );
}

function StackedChart({
  rows,
  groups,
  group,
  format,
  axisFormat,
}: {
  rows: Record<string, number | string>[];
  groups: { group: string[] }[];
  group: Group[];
  format: (value: number | null) => string;
  axisFormat: (value: number) => string;
}) {
  const config: ChartConfig = Object.fromEntries(
    groups.map((item, index) => [
      groupKey(index),
      { label: groupTitle(item.group, group), color: palette[index % palette.length] },
    ]),
  );
  return (
    <ChartContainer config={config} className={cn(inset, "aspect-auto h-72 w-full")}>
      <BarChart data={rows}>
        <CartesianGrid vertical={false} />
        <XAxis
          dataKey="day"
          tickFormatter={dayTitle}
          tickLine={false}
          axisLine={false}
          interval={0}
        />
        <YAxis tickFormatter={axisFormat} tickLine={false} axisLine={false} width={64} />
        <ChartTooltip
          content={
            <ChartTooltipContent
              labelFormatter={(label) => dayTitle(String(label))}
              formatter={rowFormatter(config, format)}
            />
          }
        />
        {group.length > 0 && <ChartLegend />}
        {groups.map((_, index) => (
          <Bar
            key={groupKey(index)}
            dataKey={groupKey(index)}
            stackId="usage"
            fill={`var(--color-${groupKey(index)})`}
            isAnimationActive={false}
          />
        ))}
      </BarChart>
    </ChartContainer>
  );
}

function UsageChart({
  usage,
  range,
  group,
  kind,
}: {
  usage: UsageView;
  range: Range;
  group: Group[];
  kind: "cost" | "tokens";
}) {
  const metric = kind === "cost" ? costOf : tokensOf;
  return (
    <StackedChart
      rows={chartRows(usage.days, usage.groups, range, (item) => metric(item.values))}
      groups={usage.groups}
      group={group}
      format={kind === "cost" ? cost : count}
      axisFormat={kind === "cost" ? cost : compact}
    />
  );
}

function ValuesRow({ title, values }: { title: string; values: UsageValues }) {
  return (
    <tr className="border-b last:border-b-0">
      <th scope="row" className="py-2 pr-4 text-left font-normal whitespace-nowrap">
        {title}
      </th>
      <td className="py-2 pr-4 text-right tabular-nums">{values.sessions}</td>
      <td className="py-2 pr-4 text-right tabular-nums">{count(values.inputTokens)}</td>
      <td className="py-2 pr-4 text-right tabular-nums">{count(values.outputTokens)}</td>
      <td className="py-2 pr-4 text-right tabular-nums">{count(values.cacheReadTokens)}</td>
      <td className="py-2 pr-4 text-right tabular-nums">{count(values.cacheWriteTokens)}</td>
      <td className="py-2 text-right tabular-nums">{cost(values.costUsd)}</td>
    </tr>
  );
}

function GroupTable({ usage, group }: { usage: UsageView; group: Group[] }) {
  const heads = ["Sessions", "Input", "Output", "Cache read", "Cache write", "Cost"];
  return (
    <div className={cn(inset, "overflow-x-auto")}>
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b text-muted-foreground">
            <th scope="col" className="py-2 pr-4 text-left font-normal">
              {groupHeader(group, groupOptions)}
            </th>
            {heads.map((head) => (
              <th key={head} scope="col" className="py-2 pr-4 text-right font-normal last:pr-0">
                {head}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {usage.groups.map((item) => (
            <ValuesRow
              key={JSON.stringify(item.group)}
              title={groupTitle(item.group, group)}
              values={item.values}
            />
          ))}
        </tbody>
      </table>
    </div>
  );
}

function UsageTab({
  usage,
  range,
  group,
  kind,
}: {
  usage: UsageView;
  range: Range;
  group: Group[];
  kind: "cost" | "tokens";
}) {
  const totals = usage.totals;
  return (
    <div className="grid gap-6">
      <Totals
        items={
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
              ]
        }
      />
      <UsageChart usage={usage} range={range} group={group} kind={kind} />
      <GroupTable usage={usage} group={group} />
    </div>
  );
}

function TimeTotals({ stats }: { stats: TimeStat[] }) {
  return (
    <dl className={cn(inset, "grid grid-cols-2 gap-4 sm:grid-cols-3")}>
      {stats.map((stat) => (
        <div key={statValue(stat)}>
          <dt className="text-sm text-muted-foreground">{statTitle(stat)}</dt>
          <dd className="text-lg font-semibold">{duration(stat.totalSeconds)}</dd>
          <dd className="text-sm text-muted-foreground">
            {stat.count} × · mean {duration(stat.meanSeconds)}
          </dd>
          {stat.results.some((item) => item.result !== "success") && (
            <dd className="text-sm text-muted-foreground">
              {stat.results
                .filter((item) => item.result !== "success")
                .map((item) => `${item.result} ${item.count}`)
                .join(" · ")}
            </dd>
          )}
        </div>
      ))}
    </dl>
  );
}

function StatMenu({
  stats,
  selected,
  onChange,
}: {
  stats: TimeStat[];
  selected?: TimeStat;
  onChange: (stat: string) => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" disabled={!selected}>
          Chart: {selected && statTitle(selected)}
          <ChevronDownIcon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start">
        <DropdownMenuRadioGroup value={selected && statValue(selected)} onValueChange={onChange}>
          {stats.map((stat) => (
            <DropdownMenuRadioItem key={statValue(stat)} value={statValue(stat)}>
              {statTitle(stat)}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function TimeTable({ times, group }: { times: UsageTimes; group: Group[] }) {
  const heads = ["Stat", "Count", "Total", "Mean"];
  return (
    <div className={cn(inset, "overflow-x-auto")}>
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b text-muted-foreground">
            <th scope="col" className="py-2 pr-4 text-left font-normal">
              {groupHeader(group, groupOptions)}
            </th>
            {heads.map((head, index) => (
              <th
                key={head}
                scope="col"
                className={cn(
                  "py-2 pr-4 font-normal last:pr-0",
                  index === 0 ? "text-left" : "text-right",
                )}
              >
                {head}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {times.groups.flatMap((item) =>
            item.stats.map((stat) => (
              <tr
                key={`${JSON.stringify(item.group)}|${statValue(stat)}`}
                className="border-b last:border-b-0"
              >
                <th scope="row" className="py-2 pr-4 text-left font-normal whitespace-nowrap">
                  {groupTitle(item.group, group)}
                </th>
                <td className="py-2 pr-4 whitespace-nowrap">{statTitle(stat)}</td>
                <td className="py-2 pr-4 text-right tabular-nums">{stat.count}</td>
                <td className="py-2 pr-4 text-right whitespace-nowrap tabular-nums">
                  {duration(stat.totalSeconds)}
                </td>
                <td className="py-2 text-right whitespace-nowrap tabular-nums">
                  {duration(stat.meanSeconds)}
                </td>
              </tr>
            )),
          )}
        </tbody>
      </table>
    </div>
  );
}

function TimesTab({
  times,
  range,
  group,
  stat,
  onStat,
}: {
  times: UsageTimes;
  range: Range;
  group: Group[];
  stat?: string;
  onStat: (stat: string) => void;
}) {
  const selected = findStat(times.totals, stat);
  const rows = chartRows(times.days, times.groups, range, (item) => {
    const found = item.stats.find(
      (candidate) => selected && statValue(candidate) === statValue(selected),
    );
    return found ? found.totalSeconds : null;
  });
  return (
    <div className="grid gap-6">
      <TimeTotals stats={times.totals} />
      <div className="grid gap-2">
        <div className={inset}>
          <StatMenu stats={times.totals} selected={selected} onChange={onStat} />
        </div>
        <StackedChart
          rows={rows}
          groups={times.groups}
          group={group}
          format={(value) => (value === null ? "—" : duration(value))}
          axisFormat={duration}
        />
      </div>
      <TimeTable times={times} group={group} />
    </div>
  );
}

function SkillTable({ rows, group }: { rows: SkillUsageRow[]; group: Group[] }) {
  const heads = ["Uses", "Had", "Used"];
  const left = ["Skill", "Source", ...(group.length > 0 ? [groupHeader(group, skillGroups)] : [])];
  return (
    <div className={cn(inset, "overflow-x-auto")}>
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b text-muted-foreground">
            {left.map((head) => (
              <th key={head} scope="col" className="py-2 pr-4 text-left font-normal">
                {head}
              </th>
            ))}
            {heads.map((head) => (
              <th key={head} scope="col" className="py-2 pr-4 text-right font-normal last:pr-0">
                {head}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.length === 0 && (
            <tr>
              <td colSpan={left.length + heads.length} className="py-2 text-muted-foreground">
                No skill uses in this period.
              </td>
            </tr>
          )}
          {rows.map((row) => (
            <tr
              key={JSON.stringify([row.skill, row.source, row.group])}
              className="border-b last:border-b-0"
            >
              <th scope="row" className="py-2 pr-4 text-left font-normal whitespace-nowrap">
                {row.skill}
              </th>
              <td className="py-2 pr-4 whitespace-nowrap">{sourceTitle(row.source)}</td>
              {group.length > 0 && (
                <td className="py-2 pr-4 whitespace-nowrap">{groupTitle(row.group, group)}</td>
              )}
              <td className="py-2 pr-4 text-right tabular-nums">{row.uses}</td>
              <td className="py-2 pr-4 text-right tabular-nums">
                {row.source === "harness" ? "—" : row.had}
              </td>
              <td className="py-2 text-right tabular-nums">{row.used}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function SkillsTab({ skills, group }: { skills: SkillUsage; group: Group[] }) {
  return (
    <div className="grid gap-6">
      <Totals
        items={[
          { title: "Uses", value: String(skills.totals.uses) },
          { title: "Skills", value: String(skills.totals.skills) },
          { title: "Sessions", value: String(skills.totals.sessions) },
        ]}
      />
      <SkillTable rows={skills.rows} group={group} />
    </div>
  );
}

export function Usage({
  search,
  onSearch,
}: {
  search: UsageSearch;
  onSearch: (patch: UsageSearch) => void;
}) {
  const showLogin = use(LoginContext);
  const [loaded, setLoaded] = useState<{ usage: UsageView; range: Range }>();
  const [loadedTimes, setLoadedTimes] = useState<{ times: UsageTimes; range: Range }>();
  const [loadedSkills, setLoadedSkills] = useState<SkillUsage>();
  const [error, setError] = useState<string>();
  const tab = search.tab ?? "cost";
  const timesTab = tab === "times";
  const skillsTab = tab === "skills";
  const groups = skillsTab ? skillGroups : groupOptions;
  const group: Group[] = groups
    .map((item) => item.key)
    .filter((key) => search.group?.includes(key));

  const [now] = useState(() => new Date());
  const query = useMemo(() => {
    const range = usageRange(search, now);
    const usageFields = groupOptions
      .map((item) => item.key)
      .filter((key) => search.group?.includes(key));
    const skillFields = skillGroups
      .map((item) => item.key)
      .filter((key) => search.group?.includes(key));
    const params: GetUsageParams = {
      from: range.from.toISOString(),
      to: range.to.toISOString(),
      tz: new Intl.DateTimeFormat().resolvedOptions().timeZone,
      harness: search.harness,
      model: search.model,
      modelVersion: search.modelVersion,
      effort: search.effort,
      role: search.role,
      repository: search.repository,
      group: usageFields.length > 0 ? usageFields : undefined,
    };
    const skillParams: GetUsageSkillsParams = {
      from: params.from,
      to: params.to,
      harness: search.harness,
      role: search.role,
      repository: search.repository,
      source: search.source,
      group: skillFields.length > 0 ? skillFields : undefined,
    };
    return { range, params, skillParams };
  }, [search, now]);

  useEffect(() => {
    if (timesTab || skillsTab) {
      return;
    }
    let current = true;
    getUsage(query.params)
      .then((res) => {
        if (!current) {
          return;
        }
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 200) {
          setLoaded({ usage: res.data.data, range: query.range });
          setError(undefined);
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => current && setError(String(err)));
    return () => {
      current = false;
    };
  }, [query, showLogin, timesTab, skillsTab]);

  useEffect(() => {
    if (!timesTab) {
      return;
    }
    let current = true;
    getUsageTimes(query.params)
      .then((res) => {
        if (!current) {
          return;
        }
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 200) {
          setLoadedTimes({ times: res.data.data, range: query.range });
          setError(undefined);
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => current && setError(String(err)));
    return () => {
      current = false;
    };
  }, [query, showLogin, timesTab]);

  useEffect(() => {
    if (!skillsTab) {
      return;
    }
    let current = true;
    getUsageSkills(query.skillParams)
      .then((res) => {
        if (!current) {
          return;
        }
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 200) {
          setLoadedSkills(res.data.data);
          setError(undefined);
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => current && setError(String(err)));
    return () => {
      current = false;
    };
  }, [query, showLogin, skillsTab]);

  return (
    <>
      <TopBar title="Usage" back="/settings" />
      <PageHeader title="Usage" />
      <Filters
        search={search}
        items={skillsTab ? skillDimensions : dimensions}
        groups={groups}
        group={group}
        options={
          skillsTab
            ? loadedSkills?.options
            : timesTab
              ? loadedTimes?.times.options
              : loaded?.usage.options
        }
        onSearch={onSearch}
      />
      {error && <ErrorBadge>{error}</ErrorBadge>}
      <Tabs
        value={tab}
        onValueChange={(value) =>
          onSearch({
            tab: value === "tokens" || value === "times" || value === "skills" ? value : undefined,
          })
        }
      >
        <TabsList className={cn("mx-4 md:mx-0")}>
          <TabsTrigger value="cost">Cost</TabsTrigger>
          <TabsTrigger value="tokens">Tokens</TabsTrigger>
          <TabsTrigger value="times">Times</TabsTrigger>
          <TabsTrigger value="skills">Skills</TabsTrigger>
        </TabsList>
        {loaded &&
          (["cost", "tokens"] as const).map((kind) => (
            <TabsContent key={kind} value={kind}>
              <UsageTab usage={loaded.usage} range={loaded.range} group={group} kind={kind} />
            </TabsContent>
          ))}
        {loadedTimes && (
          <TabsContent value="times">
            <TimesTab
              times={loadedTimes.times}
              range={loadedTimes.range}
              group={group}
              stat={search.stat}
              onStat={(stat) => onSearch({ stat })}
            />
          </TabsContent>
        )}
        {loadedSkills && (
          <TabsContent value="skills">
            <SkillsTab skills={loadedSkills} group={group} />
          </TabsContent>
        )}
      </Tabs>
    </>
  );
}
