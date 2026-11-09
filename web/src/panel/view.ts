import { cn } from "cn";
import type { Html, HtmlBuilder } from "foldkit/html";
import { renderMarkdown, beginMarkdownRender } from "@/ui/markdown";
import * as Agents from "./agents/view";
import * as Details from "./details/view";
import { Message } from "./message";
import type { Model } from "./model";
import type { Tab } from "./schemas";
import * as Recurring from "./recurring/view";
import * as Tasks from "./tasks/view";

type H = HtmlBuilder<Message>;

const tabs: readonly { tab: Tab; title: string }[] = [
  { tab: "tasks", title: "Tasks" },
  { tab: "agents", title: "Agents" },
  { tab: "details", title: "Details" },
  { tab: "brief", title: "Brief" },
  { tab: "recurring", title: "Recurring" },
];

const triggerClass =
  "relative inline-flex h-[calc(100%-1px)] flex-1 items-center justify-center gap-1.5 rounded-md border border-transparent px-1 py-0.5 text-sm font-medium whitespace-nowrap text-foreground/60 transition-all group-data-vertical/tabs:w-full group-data-vertical/tabs:justify-start hover:text-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-1 focus-visible:outline-ring disabled:pointer-events-none disabled:opacity-50 has-data-[icon=inline-end]:pr-1 has-data-[icon=inline-start]:pl-1 dark:text-muted-foreground dark:hover:text-foreground group-data-[variant=default]/tabs-list:data-active:shadow-sm group-data-[variant=line]/tabs-list:data-active:shadow-none [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4 group-data-[variant=line]/tabs-list:bg-transparent group-data-[variant=line]/tabs-list:data-active:bg-transparent dark:group-data-[variant=line]/tabs-list:data-active:border-transparent dark:group-data-[variant=line]/tabs-list:data-active:bg-transparent data-active:bg-background data-active:text-foreground dark:data-active:border-input dark:data-active:bg-input/30 dark:data-active:text-foreground after:absolute after:bg-foreground after:opacity-0 after:transition-opacity group-data-horizontal/tabs:after:inset-x-0 group-data-horizontal/tabs:after:bottom-[-5px] group-data-horizontal/tabs:after:h-0.5 group-data-vertical/tabs:after:inset-y-0 group-data-vertical/tabs:after:-right-1 group-data-vertical/tabs:after:w-0.5 group-data-[variant=line]/tabs-list:data-active:after:opacity-100";

const listClass =
  "group/tabs-list inline-flex w-fit items-center justify-center rounded-lg p-[3px] text-muted-foreground group-data-horizontal/tabs:h-8 group-data-vertical/tabs:h-fit group-data-vertical/tabs:flex-col data-[variant=line]:rounded-none gap-1 bg-transparent px-2";

const contentClass: Record<Tab, string> = {
  tasks: "overflow-y-auto p-2",
  agents: "flex flex-col overflow-y-auto p-2",
  details: "overflow-y-auto p-2",
  brief: "overflow-y-auto p-2",
  recurring: "overflow-y-auto p-2",
};

const tabId = (model: Model, tab: Tab) => `${model.instance}-tab-${tab}`;
const panelId = (model: Model, tab: Tab) => `${model.instance}-panel-${tab}`;

function renderTab(h: H, model: Model, { tab, title }: { tab: Tab; title: string }): Html {
  const active = model.tab === tab;
  return h.keyed("button")(
    tab,
    [
      h.Type("button"),
      h.Role("tab"),
      h.Id(tabId(model, tab)),
      h.AriaSelected(active),
      h.AriaControls(panelId(model, tab)),
      h.Tabindex(active ? 0 : -1),
      h.DataAttribute("slot", "tabs-trigger"),
      h.DataAttribute("state", active ? "active" : "inactive"),
      h.Class(triggerClass),
      h.OnClick(Message.SelectedTab({ tab })),
    ],
    [title],
  );
}

function renderContent(h: H, model: Model): Html {
  switch (model.tab) {
    case "tasks":
      return h.submodel({
        slotId: "tasks",
        model: model.tasks,
        view: Tasks.view,
        toParentMessage: (message) => Message.GotTasksMessage({ message }),
      });
    case "agents":
      return h.submodel({
        slotId: "agents",
        model: model.agents,
        view: Agents.view,
        toParentMessage: (message) => Message.GotAgentsMessage({ message }),
      });
    case "details":
      return h.submodel({
        slotId: "details",
        model: model.details,
        view: Details.view,
        toParentMessage: (message) => Message.GotDetailsMessage({ message }),
      });
    case "recurring":
      return h.submodel({
        slotId: "recurring",
        model: model.recurring,
        view: Recurring.view,
        toParentMessage: (message) => Message.GotRecurringMessage({ message }),
      });
    case "brief":
      return renderMarkdown(h, model.brief);
  }
}

export function view(model: Model, h: H): Html {
  beginMarkdownRender();
  return h.div(
    [
      h.DataAttribute("slot", "tabs"),
      h.DataAttribute("orientation", "horizontal"),
      h.Class("group/tabs flex gap-2 data-horizontal:flex-col min-h-0 grow"),
    ],
    [
      h.div(
        [
          h.Role("tablist"),
          h.AriaOrientation("horizontal"),
          h.DataAttribute("slot", "tabs-list"),
          h.DataAttribute("variant", "line"),
          h.DataAttribute("orientation", "horizontal"),
          h.Class(listClass),
        ],
        tabs.map((entry) => renderTab(h, model, entry)),
      ),
      h.keyed("div")(
        model.tab,
        [
          h.Role("tabpanel"),
          h.Id(panelId(model, model.tab)),
          h.AriaLabelledBy(tabId(model, model.tab)),
          h.Tabindex(0),
          h.DataAttribute("slot", "tabs-content"),
          h.DataAttribute("state", "active"),
          h.DataAttribute("orientation", "horizontal"),
          h.Class(cn("flex-1 text-sm outline-none", contentClass[model.tab])),
        ],
        [renderContent(h, model)],
      ),
    ],
  );
}
