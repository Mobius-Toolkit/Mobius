import { cn } from "cn";
import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { dayClock } from "@/lib/time";
import { badgeClass, renderBadge } from "@/ui/badge";
import { renderButton } from "@/ui/button";
import { defineDialogMount, renderDialog } from "@/ui/dialog";
import { defineRouterLink } from "@/ui/link";
import {
  inset,
  renderErrorBadge,
  renderLinkRow,
  renderList,
  renderPageHeader,
  renderRow,
  renderSection,
  rowClass,
} from "@/ui/page";
import { renderSwitch } from "@/ui/switch";
import { Message } from "./message";
import {
  fixButtonOf,
  labelsNeedYou,
  permissionsNeedYou,
  toolNeedsYou,
  toolProblem,
  workstreamsNeedYou,
  type Model,
} from "./model";
import type { Checkup, Pause, ToolCheck } from "./schemas";

type H = HtmlBuilder<Message>;

const RouterLink = defineRouterLink(Message.NavigateTo);
const DialogOpened = defineDialogMount(Message.CompletedFocusDialog);

const amber = "bg-amber-500/15 text-amber-700 dark:text-amber-400";
const muted = "text-muted-foreground text-sm";

function renderNeedsYou(h: H): Html {
  return renderBadge(h, "default", "needs you", amber);
}

function renderPageLink(
  h: H,
  options: { key: string; path: string; title: Html; busy?: boolean; children?: Html[] },
): Html {
  return renderLinkRow(h, { ...options, mount: h.OnMount(RouterLink({ path: options.path })) });
}

function renderCard(h: H, title: string, children: Html[], action?: Html): Html {
  return h.div(
    [h.Class("grid gap-6")],
    [renderPageHeader(h, title, Message.ClickedBack(), action), ...children],
  );
}

function renderOrganization(h: H, organization: string, checkup: Checkup | undefined): Html {
  const base = `/settings/checkup/${organization}`;
  const row = (name: string, title: string, needsYou: boolean) =>
    renderPageLink(h, {
      key: name,
      path: `${base}/${name}`,
      title: h.span([], [title]),
      children: checkup !== undefined && needsYou ? [renderNeedsYou(h)] : [],
    });
  return renderSection(h, organization, [
    renderList(h, [
      row("permissions", "App permissions", checkup !== undefined && permissionsNeedYou(checkup)),
      row("labels", "Labels", checkup !== undefined && labelsNeedYou(checkup)),
      row("workstreams", "Workstreams", checkup !== undefined && workstreamsNeedYou(checkup)),
    ]),
  ]);
}

function renderOverview(h: H, model: Model): Html {
  const tools = Option.getOrUndefined(model.tools);
  return h.div(
    [h.Class("grid gap-6")],
    [
      renderPageHeader(h, "Checkup"),
      renderSection(h, "Server", [
        renderList(h, [
          renderPageLink(h, {
            key: "tools",
            path: "/settings/checkup/tools",
            title: h.span([], ["Tools"]),
            busy: tools === undefined && model.toolsError === "",
            children: tools?.some(toolNeedsYou) ? [renderNeedsYou(h)] : [],
          }),
        ]),
      ]),
      ...model.organizations.map((organization) =>
        renderOrganization(h, organization, model.checkups[organization]),
      ),
    ],
  );
}

function renderToolText(h: H, tool: ToolCheck, pause: Pause | undefined, grow: boolean): Html {
  return h.span(
    [h.Class(cn("grid min-w-0", grow && "grow text-left"))],
    [
      tool.name,
      pause === undefined
        ? h.empty
        : renderBadge(
            h,
            "default",
            `usage limit until ${dayClock(pause.pausedUntil)}`,
            `w-fit ${amber}`,
          ),
      tool.status === ""
        ? h.span(
            [h.Class(`${muted} break-words`)],
            [tool.outdated ? `${tool.version} · newest ${tool.newest}` : tool.version],
          )
        : h.empty,
      tool.status !== "" && !tool.required
        ? h.span([h.Class(`${muted} break-words`)], [tool.status.replace("-", " ")])
        : h.empty,
      tool.path ? h.span([h.Class(`${muted} break-all`)], [tool.path]) : h.empty,
    ],
  );
}

function renderToolRow(h: H, tool: ToolCheck, pauses: readonly Pause[]): Html {
  const pause = tool.harness ? pauses.find((paused) => paused.harness === tool.harness) : undefined;
  const problem = toolNeedsYou(tool) ? [renderBadge(h, "destructive", toolProblem(tool))] : [];
  if (!tool.package) {
    return renderRow(h, tool.name, "justify-between", [
      renderToolText(h, tool, pause, false),
      ...problem,
    ]);
  }
  return h.keyed("li")(
    tool.name,
    [],
    [
      h.button(
        [
          h.Type("button"),
          h.Class(cn(rowClass, "w-full justify-between hover:bg-muted")),
          h.OnClick(Message.OpenedTool({ name: tool.name })),
        ],
        [renderToolText(h, tool, pause, true), ...problem],
      ),
    ],
  );
}

function renderToolDialog(h: H, model: Model, tool: ToolCheck): Html {
  return renderDialog(h, {
    mount: h.OnMount(DialogOpened()),
    titleId: "tool-title",
    title: tool.name,
    description: "",
    close: Message.ClosedTool(),
    body: h.div(
      [h.Class("grid min-w-0 content-start gap-2 overflow-y-auto px-4 pb-4")],
      [
        h.p([], ["Installed: ", h.span([h.Class("wrap-anywhere")], [tool.version])]),
        h.p([], ["Newest: ", h.span([h.Class("wrap-anywhere")], [tool.newest])]),
        model.upgradeError
          ? h.p(
              [h.Class("text-destructive wrap-anywhere whitespace-pre-wrap")],
              [model.upgradeError],
            )
          : h.empty,
      ],
    ),
    footer: h.div(
      [h.Class("grid gap-3 border-t p-4")],
      [
        renderButton(h, {
          label: "Upgrade",
          pending: model.upgrading,
          disabled: !tool.outdated || model.togglingAuto,
          className: "w-full",
          onClick: Message.ClickedUpgrade({ tool }),
        }),
        h.div(
          [h.Class("flex items-center justify-between gap-2")],
          [
            h.label(
              [
                h.For("auto-upgrade"),
                h.DataAttribute("slot", "label"),
                h.Class("flex items-center gap-2 text-sm leading-none font-medium select-none"),
              ],
              ["Auto-upgrade"],
            ),
            renderSwitch(h, {
              id: "auto-upgrade",
              checked: tool.autoUpgrade,
              disabled: model.togglingAuto || model.upgrading,
              toggle: Message.ClickedAutoUpgrade({ tool, on: !tool.autoUpgrade }),
            }),
          ],
        ),
      ],
    ),
  });
}

function renderTools(h: H, model: Model): Html {
  const tools = Option.getOrUndefined(model.tools);
  const open = Option.flatMap(model.openTool, (name) =>
    Option.fromNullishOr(tools?.find((tool) => tool.name === name)),
  );
  return renderCard(h, "Tools", [
    model.toolsError ? renderErrorBadge(h, model.toolsError) : h.empty,
    tools === undefined
      ? h.empty
      : renderList(
          h,
          tools.map((tool) => renderToolRow(h, tool, model.pauses)),
        ),
    Option.match(open, {
      onNone: () => h.empty,
      onSome: (tool) => renderToolDialog(h, model, tool),
    }),
  ]);
}

function renderPermissionStatus(h: H, permission: Checkup["permissions"][number]): Html {
  if (permission.status === "present") {
    return renderBadge(h, "secondary", "present");
  }
  return h.a(
    [
      h.DataAttribute("slot", "badge"),
      h.DataAttribute("variant", "destructive"),
      h.Attribute("href", permission.url),
      h.Attribute("target", "_blank"),
      h.Attribute("rel", "noreferrer"),
      h.Class(badgeClass("destructive")),
    ],
    [
      permission.status === "not-accepted"
        ? "not accepted: accept on GitHub"
        : "missing: add on GitHub",
    ],
  );
}

function renderPermissions(h: H, model: Model): Html {
  const checkup = model.checkups[model.organization];
  return renderCard(h, "App permissions", [
    model.checkupError ? renderErrorBadge(h, model.checkupError) : h.empty,
    checkup?.permissionsError ? renderErrorBadge(h, checkup.permissionsError) : h.empty,
    checkup !== undefined && checkup.permissions.length > 0
      ? renderList(
          h,
          checkup.permissions.map((permission) =>
            renderRow(h, permission.name, "justify-between", [
              h.span([], [`${permission.name}: ${permission.level}`]),
              renderPermissionStatus(h, permission),
            ]),
          ),
        )
      : h.empty,
  ]);
}

function renderLabelStatus(h: H, label: Checkup["repositories"][number]["labels"][number]): Html {
  switch (label.status) {
    case "present":
      return renderBadge(h, "secondary", "present");
    case "wrong-color":
      return renderBadge(h, "destructive", `wrong color: #${label.found}`);
    case "wrong-case":
      return renderBadge(h, "destructive", `wrong case: ${label.found}`);
    case "missing":
      return renderBadge(h, "destructive", "missing");
  }
}

function renderLabels(h: H, model: Model): Html {
  const checkup = model.checkups[model.organization];
  const fix = fixButtonOf(model);
  return renderCard(
    h,
    "Labels",
    [
      model.checkupError ? renderErrorBadge(h, model.checkupError) : h.empty,
      checkup?.repositories.length === 0
        ? h.p(
            [h.Class(cn("text-muted-foreground", inset))],
            ["The Mobius App has no repository in this organization."],
          )
        : h.empty,
      ...(checkup?.repositories ?? []).map((repository) =>
        renderSection(h, repository.repository, [
          renderList(
            h,
            repository.labels.map((label) =>
              renderRow(h, label.name, "justify-between", [
                h.span(
                  [h.Class("flex items-center gap-2")],
                  [
                    h.span(
                      [h.Class("size-3 rounded-full"), h.Style({ background: `#${label.color}` })],
                      [],
                    ),
                    label.name,
                  ],
                ),
                renderLabelStatus(h, label),
              ]),
            ),
          ),
        ]),
      ),
    ],
    fix === null
      ? undefined
      : renderButton(h, { label: fix.text, pending: fix.pending, onClick: Message.ClickedFix() }),
  );
}

function renderCreate(h: H, model: Model, kind: "maintenance" | "skills"): Html {
  return renderButton(h, {
    label: "Create",
    size: "sm",
    variant: "outline",
    pending: Option.exists(model.creating, (creating) => creating === kind),
    onClick: Message.ClickedCreate({ kind }),
  });
}

function renderWorkstreams(h: H, model: Model): Html {
  const checkup = model.checkups[model.organization];
  const error = model.checkupError !== "" ? model.checkupError : model.createError;
  const label = (text: string) => h.span([h.Class("whitespace-nowrap")], [text]);
  const rows = (found: Checkup) => [
    found.maintenanceWorkstream > 0
      ? renderPageLink(h, {
          key: "maintenance",
          path: `/workstreams/${found.issueRepository}/${found.maintenanceWorkstream}`,
          title: label("Maintenance Workstream"),
          children: [
            h.span([h.Class("text-muted-foreground")], [`#${found.maintenanceWorkstream}`]),
          ],
        })
      : renderRow(h, "maintenance", "justify-between", [
          label("Maintenance Workstream"),
          renderCreate(h, model, "maintenance"),
        ]),
    ...(found.skillRepository === ""
      ? []
      : [
          found.skillsWorkstream > 0
            ? renderPageLink(h, {
                key: "skills",
                path: `/workstreams/${found.skillsWorkstreamRepository}/${found.skillsWorkstream}`,
                title: label("Skills Workstream"),
                children: [
                  h.span(
                    [h.Class("text-muted-foreground")],
                    [
                      `${found.skillsWorkstreamRepository === found.issueRepository ? "" : found.skillsWorkstreamRepository}#${found.skillsWorkstream}`,
                    ],
                  ),
                ],
              })
            : renderRow(h, "skills", "justify-between", [
                label("Skills Workstream"),
                renderCreate(h, model, "skills"),
              ]),
        ]),
  ];
  return renderCard(h, "Workstreams", [
    error ? renderErrorBadge(h, error) : h.empty,
    h.p(
      [h.Class(cn("text-muted-foreground", inset))],
      [
        "The maintenance Workstream gets each task issue that Mobius creates and the CI failures of the default branch of each repository of the organization. The Skills Workstream gets the skill proposals of the agents. Create opens the chat with a draft for your approval.",
      ],
    ),
    checkup?.issueRepositoryError ? renderErrorBadge(h, checkup.issueRepositoryError) : h.empty,
    checkup !== undefined && !checkup.issueRepositoryError
      ? renderSection(h, checkup.issueRepository, [renderList(h, rows(checkup))])
      : h.empty,
  ]);
}

export function view(model: Model, h: H): Html {
  switch (model.page) {
    case "overview":
      return renderOverview(h, model);
    case "tools":
      return renderTools(h, model);
    case "permissions":
      return renderPermissions(h, model);
    case "labels":
      return renderLabels(h, model);
    case "workstreams":
      return renderWorkstreams(h, model);
  }
}
