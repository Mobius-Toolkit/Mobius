import { cn } from "cn";
import type { Html, HtmlBuilder } from "foldkit/html";

const tabsClass = "group/tabs flex flex-col gap-2";

const listClass =
  "group/tabs-list inline-flex w-fit items-center justify-center rounded-lg bg-muted p-[3px] text-muted-foreground";

const triggerClass =
  "relative inline-flex h-[calc(100%-1px)] flex-1 items-center justify-center gap-1.5 rounded-md border border-transparent px-1.5 py-0.5 text-sm font-medium whitespace-nowrap text-foreground/60 transition-all group-data-vertical/tabs:w-full group-data-vertical/tabs:justify-start hover:text-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-1 focus-visible:outline-ring disabled:pointer-events-none disabled:opacity-50 has-data-[icon=inline-end]:pr-1 has-data-[icon=inline-start]:pl-1 dark:text-muted-foreground dark:hover:text-foreground group-data-[variant=default]/tabs-list:data-active:shadow-sm group-data-[variant=line]/tabs-list:data-active:shadow-none [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4 group-data-[variant=line]/tabs-list:bg-transparent group-data-[variant=line]/tabs-list:data-active:bg-transparent dark:group-data-[variant=line]/tabs-list:data-active:border-transparent dark:group-data-[variant=line]/tabs-list:data-active:bg-transparent data-active:bg-background data-active:text-foreground dark:data-active:border-input dark:data-active:bg-input/30 dark:data-active:text-foreground after:absolute after:bg-foreground after:opacity-0 after:transition-opacity group-data-horizontal/tabs:after:inset-x-0 group-data-horizontal/tabs:after:bottom-[-5px] group-data-horizontal/tabs:after:h-0.5 group-data-vertical/tabs:after:inset-y-0 group-data-vertical/tabs:after:-right-1 group-data-vertical/tabs:after:w-0.5 group-data-[variant=line]/tabs-list:data-active:after:opacity-100";

// A row of tabs with the panel of the selected tab. Each tab has the Message that selects it. panel is the content of
// the selected tab, or empty when its data has not loaded.
export function renderTabs<M>(
  h: HtmlBuilder<M>,
  options: {
    id: string;
    listClassName?: string;
    tabs: ReadonlyArray<{ key: string; title: string; select: M }>;
    selected: string;
    panel: ReadonlyArray<Html>;
  },
): Html {
  return h.div(
    [
      h.DataAttribute("slot", "tabs"),
      h.DataAttribute("orientation", "horizontal"),
      h.Class(tabsClass),
    ],
    [
      h.div(
        [
          h.Role("tablist"),
          h.AriaOrientation("horizontal"),
          h.DataAttribute("slot", "tabs-list"),
          h.DataAttribute("variant", "default"),
          h.Class(cn(listClass, options.listClassName)),
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
              h.AriaControls(`${options.id}-panel`),
              h.DataAttribute("slot", "tabs-trigger"),
              h.DataAttribute("state", selected ? "active" : "inactive"),
              h.DataAttribute("orientation", "horizontal"),
              h.Class(triggerClass),
              h.OnClick(tab.select),
            ],
            [tab.title],
          );
        }),
      ),
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
