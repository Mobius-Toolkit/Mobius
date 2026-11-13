import { cn } from "cn";
import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { renderBadge } from "@/ui/badge";
import { buttonClass, renderButton } from "@/ui/button";
import { defineDialogMount, renderDialog } from "@/ui/dialog";
import { checkIcon, chevronRightIcon } from "@/ui/icons";
import { defineRouterLink } from "@/ui/link";
import {
  renderErrorBadge,
  renderLinkRow,
  renderList,
  renderRow,
  renderSection,
  rowClass,
} from "@/ui/page";
import { defineView } from "foldkit/submodel";
import { Message } from "./message";
import type { Model } from "./model";
import type { Checkup, Part, Picker, WorkstreamKind } from "./schemas";

type H = HtmlBuilder<Message>;

const RouterLink = defineRouterLink(Message.NavigateTo);
const DialogOpened = defineDialogMount(Message.CompletedFocusDialog);

const label = (h: H, text: string) => h.span([h.Class("whitespace-nowrap")], [text]);

function renderCreate(h: H, model: Model, kind: WorkstreamKind): Html {
  return renderButton(h, {
    label: "Create",
    size: "sm",
    variant: "outline",
    pending: Option.exists(model.creating, (creating) => creating === kind),
    onClick: Message.ClickedCreate({ kind }),
  });
}

function renderIssueRepository(h: H, checkup: Checkup): Html {
  const value = h.span(
    [h.Class("min-w-0 text-muted-foreground wrap-anywhere")],
    [checkup.issueRepositorySource === "none" ? "None" : checkup.issueRepository],
  );
  if (checkup.issueRepositorySource === "config") {
    return renderRow(h, "issue-repository", "justify-between", [
      label(h, "Issue repository"),
      value,
    ]);
  }
  return h.keyed("li")(
    "issue-repository",
    [],
    [
      h.button(
        [
          h.Type("button"),
          h.Class(cn(rowClass, "w-full justify-between hover:bg-muted")),
          h.OnClick(Message.OpenedPicker()),
        ],
        [label(h, "Issue repository"), value, chevronRightIcon(h, "size-4 shrink-0")],
      ),
    ],
  );
}

function renderMaintenance(h: H, model: Model, checkup: Checkup): Html {
  if (checkup.maintenanceWorkstream > 0) {
    return renderLinkRow(h, {
      key: "maintenance",
      path: `/workstreams/${checkup.issueRepository}/${checkup.maintenanceWorkstream}`,
      mount: h.OnMount(
        RouterLink({
          path: `/workstreams/${checkup.issueRepository}/${checkup.maintenanceWorkstream}`,
        }),
      ),
      title: label(h, "Maintenance Workstream"),
      children: [h.span([h.Class("text-muted-foreground")], [`#${checkup.maintenanceWorkstream}`])],
    });
  }
  return renderRow(h, "maintenance", "justify-between", [
    label(h, "Maintenance Workstream"),
    renderCreate(h, model, "maintenance"),
  ]);
}

function renderSkills(h: H, model: Model, checkup: Checkup): Html {
  if (checkup.skillRepository === "") {
    return renderRow(h, "skills", "justify-between", [
      h.span(
        [h.Class("grid min-w-0")],
        [
          label(h, "Skills Workstream"),
          h.span(
            [h.Class("text-sm break-words text-muted-foreground")],
            [`Create ${model.organization}/mobius-skills and give the Mobius App access to it.`],
          ),
        ],
      ),
      h.a(
        [
          h.Attribute(
            "href",
            `https://github.com/new?owner=${model.organization}&name=mobius-skills`,
          ),
          h.Attribute("target", "_blank"),
          h.Attribute("rel", "noreferrer"),
          h.Class(buttonClass({ variant: "outline", size: "sm" })),
        ],
        ["Open GitHub"],
      ),
    ]);
  }
  if (checkup.skillsWorkstream > 0) {
    const path = `/workstreams/${checkup.skillsWorkstreamRepository}/${checkup.skillsWorkstream}`;
    const repository =
      checkup.skillsWorkstreamRepository === checkup.issueRepository
        ? ""
        : checkup.skillsWorkstreamRepository;
    return renderLinkRow(h, {
      key: "skills",
      path,
      mount: h.OnMount(RouterLink({ path })),
      title: label(h, "Skills Workstream"),
      children: [
        h.span([h.Class("text-muted-foreground")], [`${repository}#${checkup.skillsWorkstream}`]),
      ],
    });
  }
  return renderRow(h, "skills", "justify-between", [
    label(h, "Skills Workstream"),
    renderCreate(h, model, "skills"),
  ]);
}

function renderRows(h: H, model: Model, checkup: Checkup, part: Part): Html[] {
  const hasIssueRepository = checkup.issueRepositorySource !== "none";
  const maintenance =
    hasIssueRepository && part !== "skills" ? [renderMaintenance(h, model, checkup)] : [];
  const skills =
    (hasIssueRepository || checkup.skillRepository === "") && part !== "maintenance"
      ? [renderSkills(h, model, checkup)]
      : [];
  return [
    ...(part === "all" || !hasIssueRepository ? [renderIssueRepository(h, checkup)] : []),
    ...maintenance,
    ...skills,
  ];
}

function renderPicker(h: H, model: Model, checkup: Checkup, picker: Picker): Html {
  return renderDialog(h, {
    mount: h.OnMount(DialogOpened()),
    titleId: "issue-repository-title",
    title: "Issue repository",
    description: `Mobius creates the Workstream issues of ${model.organization} in this repository.`,
    close: Message.ClosedPicker(),
    body: h.div(
      [
        h.Role("radiogroup"),
        h.AriaLabelledBy("issue-repository-title"),
        h.Class("grid min-w-0 content-start divide-y overflow-y-auto"),
      ],
      checkup.repositories.map(({ repository }) =>
        h.keyed("button")(
          repository,
          [
            h.Type("button"),
            h.Role("radio"),
            h.AriaChecked(repository === picker.selected),
            h.Class(cn(rowClass, "w-full justify-between hover:bg-muted md:px-4")),
            h.OnClick(Message.SelectedRepository({ repository })),
          ],
          [
            h.span([h.Class("min-w-0 text-left wrap-anywhere")], [repository]),
            repository === picker.selected ? checkIcon(h, "size-4 shrink-0") : h.empty,
          ],
        ),
      ),
    ),
    footer: h.div(
      [h.Class("grid gap-3 border-t p-4")],
      [
        picker.error
          ? renderBadge(h, "destructive", picker.error, "h-auto whitespace-normal")
          : h.empty,
        renderButton(h, {
          label: "Save",
          pending: picker.saving,
          disabled: picker.selected === "",
          className: "w-full",
          onClick: Message.ClickedSave(),
        }),
      ],
    ),
  });
}

// part is the rows to show. With no issue repository, each part also shows the row of the issue repository and no
// Create button.
export const view = defineView<Model, Message, { part: Part }>((model, { part }, h) => {
  const checkup = Option.getOrUndefined(model.checkup);
  const error = model.checkupError !== "" ? model.checkupError : model.createError;
  const rows = checkup === undefined ? [] : renderRows(h, model, checkup, part);
  return h.div(
    [h.Class("grid gap-6")],
    [
      error ? renderErrorBadge(h, error) : h.empty,
      checkup?.issueRepositoryError ? renderErrorBadge(h, checkup.issueRepositoryError) : h.empty,
      checkup === undefined
        ? h.empty
        : part === "all"
          ? renderSection(h, model.organization, [renderList(h, rows)])
          : renderList(h, rows),
      Option.match(Option.all({ checkup: model.checkup, picker: model.picker }), {
        onNone: () => h.empty,
        onSome: (open) => renderPicker(h, model, open.checkup, open.picker),
      }),
    ],
  );
});
