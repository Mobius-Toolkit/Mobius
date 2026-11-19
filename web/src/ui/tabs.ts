import { cn } from "cn";
import type { Html, HtmlBuilder } from "foldkit/html";

const tabsClass = "group/tabs flex flex-col gap-2";

const listClass =
  "group/tabs-list inline-flex w-fit items-center justify-center rounded-lg bg-muted p-[3px] text-muted-foreground";

// The line variant has a thin border below the tabs. Only the tab list scrolls sideways.
const lineListClass =
  "group/tabs-list flex min-w-0 flex-1 items-center gap-1 overflow-x-auto border-b text-muted-foreground";

const triggerClass =
  "relative inline-flex h-[calc(100%-1px)] flex-1 items-center justify-center gap-1.5 rounded-md border border-transparent px-1.5 py-0.5 text-sm font-medium whitespace-nowrap text-foreground/60 transition-all group-data-vertical/tabs:w-full group-data-vertical/tabs:justify-start hover:text-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-1 focus-visible:outline-ring disabled:pointer-events-none disabled:opacity-50 has-data-[icon=inline-end]:pr-1 has-data-[icon=inline-start]:pl-1 dark:text-muted-foreground dark:hover:text-foreground group-data-[variant=default]/tabs-list:data-active:shadow-sm group-data-[variant=line]/tabs-list:data-active:shadow-none [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4 group-data-[variant=line]/tabs-list:bg-transparent group-data-[variant=line]/tabs-list:data-active:bg-transparent dark:group-data-[variant=line]/tabs-list:data-active:border-transparent dark:group-data-[variant=line]/tabs-list:data-active:bg-transparent data-active:bg-background data-active:text-foreground dark:data-active:border-input dark:data-active:bg-input/30 dark:data-active:text-foreground after:absolute after:bg-foreground after:opacity-0 after:transition-opacity group-data-horizontal/tabs:after:inset-x-0 group-data-horizontal/tabs:after:bottom-[-5px] group-data-horizontal/tabs:after:h-0.5 group-data-vertical/tabs:after:inset-y-0 group-data-vertical/tabs:after:-right-1 group-data-vertical/tabs:after:w-0.5 group-data-[variant=line]/tabs-list:data-active:after:opacity-100";

// With the line variant, a tab is as wide as its title up to a maximum, and a longer title ends with an ellipsis.
const lineTriggerClass =
  "h-auto max-w-48 flex-none rounded-none px-3 py-2 group-data-horizontal/tabs:after:bottom-0";

// A row of tabs with the panel of the selected tab. Each tab has the Message that selects it. panel is the content of
// the selected tab, or empty when its data has not loaded. A tab with a label has it as its accessible name and title.
// end is shown after the tab list, and it does not scroll with the tabs.
export function renderTabs<M>(
  h: HtmlBuilder<M>,
  options: {
    id: string;
    variant?: "default" | "line";
    listClassName?: string;
    end?: Html;
    tabs: ReadonlyArray<{ key: string; title: string; label?: string; select: M }>;
    selected: string;
    panel: ReadonlyArray<Html>;
  },
): Html {
  const line = options.variant === "line";
  const tabList = h.div(
    [
      h.Role("tablist"),
      h.AriaOrientation("horizontal"),
      h.DataAttribute("slot", "tabs-list"),
      h.DataAttribute("variant", line ? "line" : "default"),
      h.Class(cn(line ? lineListClass : listClass, options.listClassName)),
    ],
    options.tabs.map((tab) => {
      const selected = tab.key === options.selected;
      return h.keyed("button")(
        tab.key,
        [
          h.Type("button"),
          h.Role("tab"),
          h.Id(`${options.id}-tab-${tab.key}`),
          h.AriaSelected(selected),
          ...(tab.label ? [h.AriaLabel(tab.label), h.Title(tab.label)] : []),
          h.AriaControls(`${options.id}-panel`),
          h.DataAttribute("slot", "tabs-trigger"),
          h.DataAttribute("state", selected ? "active" : "inactive"),
          h.DataAttribute("orientation", "horizontal"),
          h.Class(line ? cn(triggerClass, lineTriggerClass) : triggerClass),
          h.OnClick(tab.select),
        ],
        [line ? h.span([h.Class("truncate")], [tab.title]) : tab.title],
      );
    }),
  );
  return h.div(
    [
      h.DataAttribute("slot", "tabs"),
      h.DataAttribute("orientation", "horizontal"),
      h.Class(tabsClass),
    ],
    [
      options.end === undefined
        ? tabList
        : h.div([h.Class("flex min-w-0 items-center gap-1")], [tabList, options.end]),
      options.panel.length === 0
        ? h.empty
        : h.div(
            [
              h.Role("tabpanel"),
              h.Id(`${options.id}-panel`),
              h.AriaLabelledBy(`${options.id}-tab-${options.selected}`),
              h.Tabindex(0),
              h.DataAttribute("slot", "tabs-content"),
              h.DataAttribute("state", "active"),
              h.DataAttribute("orientation", "horizontal"),
              h.Class("flex-1 text-sm outline-none"),
            ],
            options.panel,
          ),
    ],
  );
}
