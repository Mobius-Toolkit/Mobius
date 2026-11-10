import { cn } from "cn";
import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { defineView } from "foldkit/submodel";
import { bindingText } from "@/lib/harness";
import { dayLong } from "@/lib/time";
import * as Setting from "@/setting/view";
import type { NextSetting } from "@/setting/schemas";
import { renderBadge } from "@/ui/badge";
import { buttonClass } from "@/ui/button";
import { spinnerIcon, xIcon } from "@/ui/icons";
import { defineRouterLink } from "@/ui/link";
import { defineInPlaceMount, renderSheet } from "@/ui/sheet";
import { renderSwitch } from "@/ui/switch";
import { Message } from "./message";
import {
  managed,
  selectedNames,
  type Closure,
  type McpServer,
  type McpSheet,
  type Model,
} from "./model";

type H = HtmlBuilder<Message>;

const ClosureOpened = defineInPlaceMount(Message.CompletedFocusClosure);
const McpSheetOpened = defineInPlaceMount(Message.CompletedFocusMcpSheet);
const RouterLink = defineRouterLink(Message.NavigateTo);

const mcpServersPath = "/settings/mcp-servers";

function renderBindingLine(h: H, term: string, setting: NextSetting, change: Message): Html[] {
  return [
    h.dt([h.Class("text-muted-foreground")], [term]),
    h.dd(
      [h.Class("flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1")],
      [
        h.span([h.Class("wrap-anywhere")], [bindingText(setting)]),
        h.span([h.Class("text-sm text-muted-foreground")], [setting.own ? "Workstream" : "role"]),
        h.button(
          [
            h.Type("button"),
            h.AriaLabel(`Change ${term}`),
            h.Class(buttonClass({ variant: "outline", size: "sm" })),
            h.OnClick(change),
          ],
          ["Change"],
        ),
      ],
    ),
  ];
}

function renderMcpLine(h: H, names: readonly string[]): Html[] {
  return [
    h.dt([h.Class("text-muted-foreground")], ["MCP servers"]),
    h.dd(
      [h.Class("flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1")],
      [
        h.span([h.Class("wrap-anywhere")], [names.length > 0 ? names.join(", ") : "None"]),
        h.button(
          [
            h.Type("button"),
            h.AriaLabel("Change MCP servers"),
            h.Class(buttonClass({ variant: "outline", size: "sm" })),
            h.OnClick(Message.ClickedChangeMcpServers()),
          ],
          ["Change"],
        ),
      ],
    ),
  ];
}

function renderMcpServer(h: H, server: McpServer, sheet: McpSheet): Html {
  return h.keyed("li")(
    String(server.id),
    [],
    [
      h.label(
        [h.Class("flex min-w-0 items-center gap-2 py-1")],
        [
          h.input([
            h.Type("checkbox"),
            h.Checked(sheet.selected.includes(server.id)),
            h.Disabled(sheet.saving),
            h.Class("size-4 shrink-0 accent-primary"),
            h.OnClick(Message.ToggledMcpServer({ id: server.id })),
          ]),
          h.span([h.Class("min-w-0 wrap-anywhere")], [server.name]),
          renderBadge(h, "secondary", server.kind === "http" ? "HTTP" : "stdio", "shrink-0"),
        ],
      ),
    ],
  );
}

function renderMcpSheet(h: H, model: Model, servers: readonly McpServer[], sheet: McpSheet): Html {
  return renderSheet(h, {
    mount: h.OnMount(McpSheetOpened()),
    titleId: "mcp-servers-title",
    title: `MCP servers of #${model.number}`,
    description: {
      id: "mcp-servers-description",
      text: "The Operator of the Workstream uses the selected servers.",
    },
    close: Message.ClosedMcpServers(),
    className: "gap-3 p-4 pt-3 md:mx-auto md:max-w-md md:rounded-t-xl",
    children: [
      sheet.error
        ? renderBadge(h, "destructive", sheet.error, "h-auto whitespace-normal")
        : h.empty,
      servers.length > 0
        ? h.ul(
            [h.Class("grid max-h-[50dvh] overflow-y-auto")],
            servers.map((server) => renderMcpServer(h, server, sheet)),
          )
        : h.div(
            [h.Class("grid gap-1")],
            [
              h.p([h.Class("text-muted-foreground")], ["No MCP servers."]),
              h.a(
                [
                  h.Attribute("href", mcpServersPath),
                  h.OnMount(RouterLink({ path: mcpServersPath })),
                  h.Class("w-fit text-primary underline underline-offset-4"),
                ],
                ["Add a server"],
              ),
            ],
          ),
      h.div(
        [h.Class("flex items-center justify-end gap-2")],
        [
          h.button(
            [
              h.Type("button"),
              h.Class(buttonClass({ variant: "outline" })),
              h.OnClick(Message.ClosedMcpServers()),
            ],
            ["Cancel"],
          ),
          h.button(
            [
              h.Type("button"),
              h.Disabled(sheet.saving),
              h.Class(buttonClass({})),
              h.OnClick(Message.ClickedSaveMcpServers()),
            ],
            [sheet.saving ? spinnerIcon(h) : h.empty, "Save"],
          ),
        ],
      ),
    ],
  });
}

function renderConfirmation(h: H, model: Model, closure: Closure): Html {
  return h.div(
    [h.DataAttribute("slot", "dialog-portal"), h.Class("contents"), h.OnMount(ClosureOpened())],
    [
      h.div([
        h.DataAttribute("slot", "dialog-overlay"),
        h.Class(
          "fixed inset-0 isolate z-50 bg-black/10 duration-100 supports-backdrop-filter:backdrop-blur-xs",
        ),
        h.OnClick(Message.DismissedClosure()),
      ]),
      h.div(
        [
          h.Role("dialog"),
          h.Attribute("aria-modal", "true"),
          h.Tabindex(-1),
          h.AriaLabelledBy("closure-title"),
          h.AriaDescribedBy("closure-description"),
          h.DataAttribute("slot", "dialog-content"),
          h.Class(
            cn(
              "fixed top-1/2 left-1/2 z-50 grid w-full max-w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 gap-4 rounded-xl bg-popover p-4 text-sm text-popover-foreground ring-1 ring-foreground/10 duration-100 outline-none sm:max-w-sm",
              "flex max-h-[calc(100svh-2rem)] flex-col sm:max-w-lg",
            ),
          ),
        ],
        [
          h.div(
            [h.DataAttribute("slot", "dialog-header"), h.Class("flex flex-col gap-2")],
            [
              h.h2(
                [
                  h.Id("closure-title"),
                  h.DataAttribute("slot", "dialog-title"),
                  h.Class("font-heading text-base leading-none font-medium"),
                ],
                ["Close the Workstream"],
              ),
              h.p(
                [
                  h.Id("closure-description"),
                  h.DataAttribute("slot", "dialog-description"),
                  h.Class("text-sm text-muted-foreground"),
                ],
                [
                  'Mobius closes these items as "won\'t do". Each item gets a comment and the label mobius:wont-do.',
                ],
              ),
            ],
          ),
          h.div(
            [h.Class("grid min-h-0 gap-2 overflow-y-auto")],
            [
              closure.error
                ? renderBadge(
                    h,
                    "destructive",
                    closure.error,
                    "h-auto w-full justify-start whitespace-normal",
                  )
                : h.empty,
              Option.isNone(closure.items) && !closure.error
                ? h.p([h.Class("text-muted-foreground")], ["Mobius reads the items."])
                : h.empty,
              h.ul(
                [h.Class("grid gap-1")],
                Option.getOrElse(closure.items, () => []).map((item) =>
                  h.keyed("li")(
                    `${item.kind} ${item.repository}#${item.number}`,
                    [h.Class("flex items-baseline gap-2")],
                    [
                      renderBadge(
                        h,
                        "secondary",
                        item.kind === "pullRequest" ? "pull request" : "issue",
                        "shrink-0",
                      ),
                      h.span(
                        [],
                        [
                          `${item.repository === `${model.owner}/${model.name}` ? "" : item.repository}#${item.number} ${item.title}`,
                        ],
                      ),
                    ],
                  ),
                ),
              ),
            ],
          ),
          h.div(
            [
              h.DataAttribute("slot", "dialog-footer"),
              h.Class(
                "-mx-4 -mb-4 flex flex-col-reverse gap-2 rounded-b-xl border-t bg-muted/50 p-4 sm:flex-row sm:justify-end",
              ),
            ],
            [
              h.button(
                [
                  h.Type("button"),
                  h.Class(buttonClass({ variant: "outline" })),
                  h.OnClick(Message.DismissedClosure()),
                ],
                ["Cancel"],
              ),
              h.button(
                [
                  h.Type("button"),
                  h.Disabled(Option.isNone(closure.items) || closure.busy),
                  h.Class(buttonClass({ variant: "destructive" })),
                  h.OnClick(Message.ClickedCloseWorkstream()),
                ],
                [closure.busy ? spinnerIcon(h) : h.empty, "Close as won't do"],
              ),
            ],
          ),
          h.button(
            [
              h.Type("button"),
              h.DataAttribute("slot", "dialog-close"),
              h.Class(
                buttonClass({
                  variant: "ghost",
                  size: "icon-sm",
                  className: "absolute top-2 right-2",
                }),
              ),
              h.OnClick(Message.DismissedClosure()),
            ],
            [xIcon(h), h.span([h.Class("sr-only")], ["Close"])],
          ),
        ],
      ),
    ],
  );
}

export const view = defineView<Model, Message>((model, h) => {
  const evergreenId = `${model.instance}-evergreen`;
  const details = Option.getOrUndefined(model.details);
  const servers = Option.getOrUndefined(model.mcpServers);
  const locked = details === undefined || managed(details) || model.evergreenBusy;
  return h.div(
    [h.Class("grid gap-4 px-2")],
    [
      model.error ? renderBadge(h, "destructive", model.error) : h.empty,
      details
        ? h.dl(
            [h.Class("grid grid-cols-[auto_1fr] gap-x-6 gap-y-2")],
            [
              h.dt([h.Class("text-muted-foreground")], ["Created"]),
              h.dd([], [dayLong(details.createdAt)]),
              h.dt([h.Class("text-muted-foreground")], ["Completed tasks"]),
              h.dd([], [String(details.completedTasks)]),
              h.dt([h.Class("text-muted-foreground")], ["Open tasks"]),
              h.dd([], [String(details.openTasks)]),
              ...renderBindingLine(
                h,
                "Lead",
                details.lead,
                Message.ClickedChange({ session: "lead" }),
              ),
              ...renderBindingLine(
                h,
                "Implementer",
                details.implementer,
                Message.ClickedChange({ session: "implementer" }),
              ),
              ...renderBindingLine(
                h,
                "Operator",
                details.operator,
                Message.ClickedChange({ session: "operator" }),
              ),
              ...(servers ? renderMcpLine(h, selectedNames(servers, details.mcpServers)) : []),
            ],
          )
        : h.empty,
      Option.match(Option.all({ servers: model.mcpServers, sheet: model.mcpSheet }), {
        onNone: () => h.empty,
        onSome: (open) => renderMcpSheet(h, model, open.servers, open.sheet),
      }),
      Option.match(model.sheet, {
        onNone: () => h.empty,
        onSome: ({ setting }) =>
          h.submodel({
            slotId: "setting",
            model: setting,
            view: Setting.view,
            toParentMessage: (message) => Message.GotSettingMessage({ message }),
          }),
      }),
      h.div(
        [h.Class("grid gap-1")],
        [
          h.div(
            [h.Class("flex items-center gap-2")],
            [
              renderSwitch(h, {
                id: evergreenId,
                checked: details?.evergreen ?? false,
                disabled: locked,
                toggle: Message.SwitchedEvergreen({ on: !details?.evergreen }),
              }),
              h.label(
                [
                  h.For(evergreenId),
                  h.DataAttribute("slot", "label"),
                  h.Class(
                    "flex items-center gap-2 text-sm leading-none font-medium select-none peer-disabled:cursor-not-allowed peer-disabled:opacity-50",
                  ),
                ],
                ["Evergreen"],
              ),
            ],
          ),
          details && managed(details)
            ? h.p(
                [h.Class("text-sm text-muted-foreground")],
                ["Mobius manages this Workstream, so this value cannot change."],
              )
            : h.empty,
        ],
      ),
      h.button(
        [
          h.Type("button"),
          h.Class(buttonClass({ variant: "destructive", className: "justify-self-start" })),
          h.OnClick(Message.ClickedClose()),
        ],
        ["Close"],
      ),
      Option.match(model.closure, {
        onNone: () => h.empty,
        onSome: (closure) => renderConfirmation(h, model, closure),
      }),
    ],
  );
});
