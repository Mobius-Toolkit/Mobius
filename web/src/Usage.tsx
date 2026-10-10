import { ChevronDownIcon } from "lucide-react";
import { use, useEffect, useMemo, useState } from "react";
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts";
import {
  getUsage,
  type GetUsageParams,
  type Usage as UsageView,
  type UsageValues,
} from "@/api/api.gen";
import { ErrorBadge, inset, PageHeader } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
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
  formatDay,
  groupKey,
  palette,
  periods,
  tokensOf,
  usageRange,
  type Dimension,
  type Period,
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

function GroupMenu({
  group,
  onChange,
}: {
  group?: Dimension;
  onChange: (group?: Dimension) => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline">
          Group by: {dimensions.find((item) => item.key === group)?.title ?? "None"}
          <ChevronDownIcon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start">
        <DropdownMenuRadioGroup
          value={group ?? ""}
          onValueChange={(value) => onChange(dimensions.find((item) => item.key === value)?.key)}
        >
          <DropdownMenuRadioItem value="">None</DropdownMenuRadioItem>
          {dimensions.map((item) => (
            <DropdownMenuRadioItem key={item.key} value={item.key}>
              {item.title}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function Filters({
  search,
  options,
  onSearch,
}: {
  search: UsageSearch;
  options?: UsageView["options"];
  onSearch: (patch: UsageSearch) => void;
}) {
  return (
    <div className={cn(inset, "grid gap-2")}>
      <div className="flex flex-wrap gap-2">
        <PeriodMenu search={search} onSearch={onSearch} />
        {dimensions.map((item) => (
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
        <GroupMenu group={search.group} onChange={(group) => onSearch({ group })} />
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

// A row with no value for the group has the empty group. With no group, the only row has all values.
function groupTitle(group: string, selected?: Dimension) {
  if (selected === undefined) {
    return "All";
  }
  return group || "—";
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

function UsageChart({
  usage,
  range,
  group,
  kind,
}: {
  usage: UsageView;
  range: Range;
  group?: Dimension;
  kind: "cost" | "tokens";
}) {
  const rows = chartRows(usage, range, kind === "cost" ? costOf : tokensOf);
  const format = kind === "cost" ? cost : count;
  const config: ChartConfig = Object.fromEntries(
    usage.groups.map((item, index) => [
      groupKey(index),
      { label: groupTitle(item.group, group), color: palette[index % palette.length] },
    ]),
  );
  return (
    <ChartContainer config={config} className={cn(inset, "aspect-auto h-72 w-full")}>
      <BarChart data={rows}>
        <CartesianGrid vertical={false} />
        <XAxis dataKey="day" tickFormatter={dayTitle} tickLine={false} axisLine={false} />
        <YAxis
          tickFormatter={(value: number) => (kind === "cost" ? cost(value) : compact(value))}
          tickLine={false}
          axisLine={false}
          width={64}
        />
        <ChartTooltip
          content={
            <ChartTooltipContent
              labelFormatter={(label) => dayTitle(String(label))}
              formatter={rowFormatter(config, format)}
            />
          }
        />
        {group && <ChartLegend content={<ChartLegendContent />} />}
        {usage.groups.map((_, index) => (
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

function GroupTable({ usage, group }: { usage: UsageView; group?: Dimension }) {
  const heads = ["Sessions", "Input", "Output", "Cache read", "Cache write", "Cost"];
  return (
    <div className={cn(inset, "overflow-x-auto")}>
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b text-muted-foreground">
            <th scope="col" className="py-2 pr-4 text-left font-normal">
              {dimensions.find((item) => item.key === group)?.title ?? "Group"}
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
              key={item.group}
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
  group?: Dimension;
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

export function Usage({
  search,
  onSearch,
}: {
  search: UsageSearch;
  onSearch: (patch: UsageSearch) => void;
}) {
  const showLogin = use(LoginContext);
  const [loaded, setLoaded] = useState<{ usage: UsageView; range: Range }>();
  const [error, setError] = useState<string>();

  const [now] = useState(() => new Date());
  const query = useMemo(() => {
    const range = usageRange(search, now);
    const params: GetUsageParams = {
      from: range.from.toISOString(),
      to: range.to.toISOString(),
      tz: new Intl.DateTimeFormat().resolvedOptions().timeZone,
      harness: search.harness,
      model: search.model,
      effort: search.effort,
      role: search.role,
      repository: search.repository,
      group: search.group,
    };
    return { range, params };
  }, [search, now]);

  useEffect(() => {
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
  }, [query, showLogin]);

  const tab = search.tab ?? "cost";
  return (
    <>
      <TopBar title="Usage" back="/settings" />
      <PageHeader title="Usage" />
      <Filters search={search} options={loaded?.usage.options} onSearch={onSearch} />
      {error && <ErrorBadge>{error}</ErrorBadge>}
      <Tabs
        value={tab}
        onValueChange={(value) => onSearch({ tab: value === "tokens" ? "tokens" : undefined })}
      >
        <TabsList className={cn("mx-4 md:mx-0")}>
          <TabsTrigger value="cost">Cost</TabsTrigger>
          <TabsTrigger value="tokens">Tokens</TabsTrigger>
        </TabsList>
        {loaded &&
          (["cost", "tokens"] as const).map((kind) => (
            <TabsContent key={kind} value={kind}>
              <UsageTab
                usage={loaded.usage}
                range={loaded.range}
                group={search.group}
                kind={kind}
              />
            </TabsContent>
          ))}
      </Tabs>
    </>
  );
}
