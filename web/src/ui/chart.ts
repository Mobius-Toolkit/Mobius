import { Effect, Queue, Schema, Stream } from "effect";
import { Mount } from "foldkit";
import type { Html, HtmlBuilder } from "foldkit/html";

// A row is one category of the horizontal axis. values has one value for each series, and null has no mark.
export type ChartRow = { key: string; title: string; values: ReadonlyArray<number | null> };
export type ChartSeries = { label: string; color: string };
export type ChartSize = { width: number; height: number };
export type ChartPoint = { x: number; y: number };

const margin = 5;
const yAxisWidth = 64;
const xAxisHeight = 30;
const tickCount = 5;
const barGap = 0.1;
const linePadding = 12;
// The label of the top tick moves down by half of its height, so that it stays inside the chart.
const topLabelShift = 4;

// The Mount of the box of a chart. resized gives the size of the box, hovered gives the position of the pointer in the
// box, and left gives no value when a mouse leaves the box. A touch keeps its last position.
export function defineChartMount<
  Resized extends Schema.Top & ((size: ChartSize) => Schema.Schema.Type<Resized>),
  Hovered extends Schema.Top & ((point: ChartPoint) => Schema.Schema.Type<Hovered>),
  Left extends Schema.Top & (() => Schema.Schema.Type<Left>),
>(resized: Resized, hovered: Hovered, left: Left) {
  return Mount.defineStream("ChartBox", {
    messages: [resized, hovered, left],
    execute: ({ element }) =>
      Stream.callback<
        Schema.Schema.Type<Resized> | Schema.Schema.Type<Hovered> | Schema.Schema.Type<Left>
      >((queue) => {
        const observer = new ResizeObserver(() =>
          Queue.offerUnsafe(
            queue,
            resized({ width: element.clientWidth, height: element.clientHeight }),
          ),
        );
        const point = (event: Event) => {
          if (event instanceof PointerEvent) {
            const box = element.getBoundingClientRect();
            Queue.offerUnsafe(
              queue,
              hovered({ x: event.clientX - box.left, y: event.clientY - box.top }),
            );
          }
        };
        const leave = (event: Event) => {
          if (event instanceof PointerEvent && event.pointerType === "mouse") {
            Queue.offerUnsafe(queue, left());
          }
        };
        return Effect.gen(function* () {
          observer.observe(element);
          element.addEventListener("pointermove", point);
          element.addEventListener("pointerdown", point);
          element.addEventListener("pointerleave", leave);
          yield* Effect.addFinalizer(() =>
            Effect.sync(() => {
              observer.disconnect();
              element.removeEventListener("pointermove", point);
              element.removeEventListener("pointerdown", point);
              element.removeEventListener("pointerleave", leave);
            }),
          );
        });
      }),
  });
}

const rounded = (value: number) => Number(value.toPrecision(12));

// The ticks of a value axis that starts at 0. They are five equal steps, and the last one is at least the largest value.
// The step is the first step of 1, 2, 2.5 and 5 times a power of ten that five ticks cover.
export function niceTicks(largest: number): number[] {
  if (largest <= 0) {
    return [0, 1, 2, 3, 4];
  }
  const rough = largest / (tickCount - 1);
  const digits = Math.floor(Math.log10(rough)) + 1;
  const unit = 10 ** digits;
  const scale = digits === 1 ? 0.1 : 0.05;
  let correction = 0;
  for (;;) {
    const step = rounded((Math.ceil(rounded(rough / unit / scale)) + correction) * scale * unit);
    if (Math.ceil(rounded(largest / step)) + 1 <= tickCount) {
      return Array.from({ length: tickCount }, (_, index) => rounded(index * step));
    }
    correction += 1;
  }
}

const sumOf = (row: ChartRow) => row.values.reduce<number>((sum, value) => sum + (value ?? 0), 0);

const largestOf = (row: ChartRow) => row.values.reduce<number>((a, b) => Math.max(a, b ?? 0), 0);

type Plot = { left: number; right: number; top: number; bottom: number };

function plotOf(size: ChartSize, legend: boolean): Plot {
  return {
    left: margin + yAxisWidth,
    right: size.width - margin,
    top: margin,
    bottom: size.height - (legend ? 0 : margin) - xAxisHeight,
  };
}

type Layout<M> = {
  h: HtmlBuilder<M>;
  plot: Plot;
  ticks: ReadonlyArray<number>;
  axisFormat: (value: number) => string;
  y: (value: number) => number;
};

const attribute = <M>(h: HtmlBuilder<M>, name: string, value: number | string) =>
  h.Attribute(name, String(value));

function renderGrid<M>({ h, plot, ticks, axisFormat, y }: Layout<M>): Html {
  return h.g(
    [],
    [
      ...ticks.map((tick) =>
        h.keyed("line")(
          `grid-${tick}`,
          [
            h.Class("stroke-border/50"),
            attribute(h, "x1", plot.left),
            attribute(h, "x2", plot.right),
            attribute(h, "y1", y(tick)),
            attribute(h, "y2", y(tick)),
          ],
          [],
        ),
      ),
      h.g(
        [h.DataAttribute("slot", "chart-y-axis")],
        ticks.map((tick) =>
          h.keyed("text")(
            `y-${tick}`,
            [
              h.Class("fill-muted-foreground"),
              h.Attribute("text-anchor", "end"),
              attribute(h, "x", plot.left - 8),
              attribute(h, "y", y(tick) + (tick === ticks[ticks.length - 1] ? topLabelShift : 0)),
            ],
            [
              h.tspan(
                [attribute(h, "x", plot.left - 8), h.Attribute("dy", "0.355em")],
                [axisFormat(tick)],
              ),
            ],
          ),
        ),
      ),
    ],
  );
}

function renderXAxis<M>(
  h: HtmlBuilder<M>,
  plot: Plot,
  rows: ReadonlyArray<ChartRow>,
  x: (index: number) => number,
): Html {
  return h.g(
    [h.DataAttribute("slot", "chart-x-axis")],
    rows.map((row, index) =>
      h.keyed("text")(
        `x-${row.key}`,
        [
          h.Class("fill-muted-foreground"),
          h.Attribute("text-anchor", "middle"),
          attribute(h, "x", x(index)),
          attribute(h, "y", plot.bottom + 8),
        ],
        [h.tspan([attribute(h, "x", x(index)), h.Attribute("dy", "0.71em")], [row.title])],
      ),
    ),
  );
}

function renderTooltip<M>(
  h: HtmlBuilder<M>,
  options: {
    width: number;
    point: ChartPoint;
    row: ChartRow;
    series: ReadonlyArray<ChartSeries>;
    valueFormat: (value: number) => string;
  },
): Html {
  const entries = options.series.flatMap((item, index) => {
    const value = options.row.values[index];
    return value === null || value === undefined ? [] : [{ item, value }];
  });
  if (entries.length === 0) {
    return h.empty;
  }
  const flip = options.point.x > options.width / 2;
  return h.div(
    [
      h.DataAttribute("slot", "chart-tooltip"),
      h.Class(
        "pointer-events-none absolute z-10 grid min-w-32 items-start gap-1.5 rounded-lg border border-border/50 bg-background px-2.5 py-1.5 text-xs shadow-xl",
      ),
      h.Style({
        left: `${flip ? options.point.x - 10 : options.point.x + 10}px`,
        top: `${options.point.y + 10}px`,
        transform: flip ? "translateX(-100%)" : "none",
      }),
    ],
    [
      h.div([h.Class("font-medium")], [options.row.title]),
      h.div(
        [h.Class("grid gap-1.5")],
        entries.map(({ item, value }) =>
          h.keyed("div")(
            item.label,
            [h.Class("flex w-full flex-wrap items-center gap-2")],
            [
              h.span([
                h.Class("size-2.5 shrink-0 rounded-[2px]"),
                h.Style({ background: item.color }),
              ]),
              h.span([h.Class("grow text-muted-foreground")], [item.label]),
              h.span([h.Class("font-mono font-medium tabular-nums")], [options.valueFormat(value)]),
            ],
          ),
        ),
      ),
    ],
  );
}

function renderLegend<M>(h: HtmlBuilder<M>, series: ReadonlyArray<ChartSeries>): Html {
  return h.div(
    [
      h.DataAttribute("slot", "chart-legend"),
      h.Class("mb-[5px] flex items-center justify-center gap-4 pt-3"),
    ],
    series.map((item) =>
      h.keyed("div")(
        item.label,
        [h.Class("flex items-center gap-1.5")],
        [
          h.div([h.Class("h-2 w-2 shrink-0 rounded-[2px]"), h.Style({ background: item.color })]),
          item.label,
        ],
      ),
    ),
  );
}

type ChartOptions<M> = {
  mount: ReturnType<HtmlBuilder<M>["OnMount"]>;
  className: string;
  size: ChartSize;
  hover: ChartPoint | undefined;
  rows: ReadonlyArray<ChartRow>;
  series: ReadonlyArray<ChartSeries>;
  legend: boolean;
  axisFormat: (value: number) => string;
  valueFormat: (value: number) => string;
  ticks?: ReadonlyArray<number>;
};

type Shape<M> = {
  largest: (rows: ReadonlyArray<ChartRow>) => number;
  x: (plot: Plot, count: number) => (index: number) => number;
  active: (plot: Plot, count: number, pointerX: number) => number;
  render: (
    layout: Layout<M>,
    options: ChartOptions<M>,
    x: (index: number) => number,
    active: number | undefined,
  ) => Html;
};

function renderChart<M>(h: HtmlBuilder<M>, shape: Shape<M>, options: ChartOptions<M>): Html {
  const { size, rows } = options;
  const plot = plotOf(size, options.legend);
  const ticks = options.ticks ?? niceTicks(shape.largest(rows));
  const top = ticks[ticks.length - 1] ?? 1;
  const y = (value: number) => plot.bottom - (value / top) * (plot.bottom - plot.top);
  const x = shape.x(plot, rows.length);
  const hover = options.hover;
  const inside =
    hover !== undefined &&
    hover.x >= plot.left &&
    hover.x <= plot.right &&
    hover.y >= plot.top &&
    hover.y <= plot.bottom;
  const active = inside ? shape.active(plot, rows.length, hover.x) : undefined;
  const row = active === undefined ? undefined : rows[active];
  const layout = { h, plot, ticks, axisFormat: options.axisFormat, y };
  return h.div(
    [h.DataAttribute("slot", "chart"), h.Class(`flex flex-col text-xs ${options.className}`)],
    [
      h.div(
        [options.mount, h.Class("relative min-h-0 grow touch-pan-y")],
        [
          size.width === 0
            ? h.empty
            : h.svg(
                [
                  h.Class("absolute inset-0"),
                  attribute(h, "width", size.width),
                  attribute(h, "height", size.height),
                ],
                [
                  renderGrid(layout),
                  shape.render(layout, options, x, active),
                  renderXAxis(h, plot, rows, x),
                ],
              ),
          hover !== undefined && row !== undefined
            ? renderTooltip(h, {
                width: size.width,
                point: hover,
                row,
                series: options.series,
                valueFormat: options.valueFormat,
              })
            : h.empty,
        ],
      ),
      options.legend ? renderLegend(h, options.series) : h.empty,
    ],
  );
}

const bandOf = (plot: Plot, count: number) => (plot.right - plot.left) / count;

function barShape<M>(): Shape<M> {
  return {
    largest: (rows) => rows.reduce((a, row) => Math.max(a, sumOf(row)), 0),
    x: (plot, count) => (index) => plot.left + bandOf(plot, count) * (index + 0.5),
    active: (plot, count, pointerX) =>
      Math.min(count - 1, Math.floor((pointerX - plot.left) / bandOf(plot, count))),
    render: ({ h, plot, y }, options, x, active) => {
      const band = bandOf(plot, options.rows.length);
      return h.g(
        [h.DataAttribute("slot", "chart-bars")],
        [
          active === undefined
            ? h.empty
            : h.rect(
                [
                  h.Class("fill-muted"),
                  attribute(h, "x", x(active) - band / 2),
                  attribute(h, "y", plot.top),
                  attribute(h, "width", band),
                  attribute(h, "height", plot.bottom - plot.top),
                ],
                [],
              ),
          ...options.rows.flatMap((row, index) => {
            let base = 0;
            return row.values.flatMap((value, seriesIndex) => {
              if (value === null) {
                return [];
              }
              const from = base;
              base += value;
              return [
                h.keyed("rect")(
                  `${row.key}-${seriesIndex}`,
                  [
                    h.DataAttribute("slot", "chart-bar"),
                    attribute(h, "x", x(index) - band * (0.5 - barGap)),
                    attribute(h, "y", y(base)),
                    attribute(h, "width", band * (1 - 2 * barGap)),
                    attribute(h, "height", y(from) - y(base)),
                    attribute(h, "fill", options.series[seriesIndex]?.color ?? "none"),
                  ],
                  [],
                ),
              ];
            });
          }),
        ],
      );
    },
  };
}

const lineX = (plot: Plot, count: number) => (index: number) =>
  count === 1
    ? (plot.left + plot.right) / 2
    : plot.left + linePadding + (index * (plot.right - plot.left - 2 * linePadding)) / (count - 1);

function lineShape<M>(): Shape<M> {
  return {
    largest: (rows) => rows.reduce((a, row) => Math.max(a, largestOf(row)), 0),
    x: lineX,
    active: (plot, count, pointerX) => {
      const at = lineX(plot, count);
      let nearest = 0;
      for (let index = 1; index < count; index += 1) {
        if (Math.abs(at(index) - pointerX) < Math.abs(at(nearest) - pointerX)) {
          nearest = index;
        }
      }
      return nearest;
    },
    render: ({ h, plot, y }, options, x, active) =>
      h.g(
        [h.DataAttribute("slot", "chart-lines")],
        [
          active === undefined
            ? h.empty
            : h.line(
                [
                  h.Class("stroke-border"),
                  attribute(h, "x1", x(active)),
                  attribute(h, "x2", x(active)),
                  attribute(h, "y1", plot.top),
                  attribute(h, "y2", plot.bottom),
                ],
                [],
              ),
          ...options.series.flatMap((item, seriesIndex) => {
            const points = options.rows.flatMap((row, index) => {
              const value = row.values[seriesIndex];
              return value === null || value === undefined ? [] : [{ index, value }];
            });
            const segments: (typeof points)[] = [];
            for (const point of points) {
              const last = segments[segments.length - 1];
              if (last && last[last.length - 1]?.index === point.index - 1) {
                last.push(point);
              } else {
                segments.push([point]);
              }
            }
            return [
              ...segments.map((segment) =>
                h.keyed("path")(
                  `line-${seriesIndex}-${segment[0]?.index}`,
                  [
                    h.DataAttribute("slot", "chart-line"),
                    h.Attribute("fill", "none"),
                    h.Attribute("stroke", item.color),
                    h.Attribute("stroke-width", "2"),
                    h.Attribute(
                      "d",
                      segment
                        .map(
                          (point, at) =>
                            `${at === 0 ? "M" : "L"}${x(point.index)},${y(point.value)}`,
                        )
                        .join(""),
                    ),
                  ],
                  [],
                ),
              ),
              ...points.map((point) =>
                h.keyed("circle")(
                  `dot-${seriesIndex}-${point.index}`,
                  [
                    h.DataAttribute("slot", "chart-dot"),
                    h.Attribute("r", "3"),
                    h.Attribute("fill", "#fff"),
                    h.Attribute("stroke", item.color),
                    h.Attribute("stroke-width", "2"),
                    attribute(h, "cx", x(point.index)),
                    attribute(h, "cy", y(point.value)),
                  ],
                  [],
                ),
              ),
            ];
          }),
        ],
      ),
  };
}

// The bars of one row are stacked in the order of the series.
export function renderBarChart<M>(h: HtmlBuilder<M>, options: ChartOptions<M>): Html {
  return renderChart(h, barShape<M>(), options);
}

export function renderLineChart<M>(h: HtmlBuilder<M>, options: ChartOptions<M>): Html {
  return renderChart(h, lineShape<M>(), options);
}
