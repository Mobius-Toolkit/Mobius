import { cn } from "cn";
import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { dayClock } from "@/lib/time";
import { renderBadge } from "@/ui/badge";
import { renderButton } from "@/ui/button";
import { defineDialogMount, renderDialog } from "@/ui/dialog";
import { defineRouterLink } from "@/ui/link";
import { beginMarkdownRender, renderMarkdown } from "@/ui/markdown";
import { inset, renderLinkRow, renderList, renderPageHeader, renderSection } from "@/ui/page";
import { textareaClass } from "@/ui/textarea";
import { Message } from "./message";
import {
  currentText,
  fileTitle,
  lineText,
  pageTitle,
  repositoriesOf,
  versionsOf,
  type Editing,
  type Model,
  type ShownVersion,
} from "./model";

type H = HtmlBuilder<Message>;

const RouterLink = defineRouterLink(Message.NavigateTo);
const DialogOpened = defineDialogMount(Message.CompletedFocusDialog);

const box = "rounded-lg border p-3 text-sm";

function renderRepositories(h: H, model: Model): Html {
  return h.div(
    [h.Class("grid gap-6")],
    [
      renderPageHeader(h, "Memory"),
      ...model.organizations.map((organization) => {
        const repositories = repositoriesOf(model, organization);
        return renderSection(h, organization, [
          repositories.length === 0
            ? h.p(
                [h.Class(cn("text-muted-foreground", inset))],
                ["The Mobius App has no repository in this organization."],
              )
            : h.empty,
          renderList(
            h,
            repositories.map((repository) => {
              const path = `/settings/memory/${repository}`;
              return renderLinkRow(h, {
                key: repository,
                path,
                mount: h.OnMount(RouterLink({ path })),
                title: h.span([], [repository]),
              });
            }),
          ),
        ]);
      }),
    ],
  );
}

function renderFiles(h: H, model: Model): Html {
  return h.div(
    [h.Class("grid gap-6")],
    [
      renderPageHeader(
        h,
        pageTitle({ page: "files", owner: model.owner, name: model.name, file: "" }),
        Message.ClickedBack(),
      ),
      model.error ? h.div([h.Class(inset)], [renderBadge(h, "destructive", model.error)]) : h.empty,
      Option.match(model.files, {
        onNone: () => h.empty,
        onSome: (files) =>
          renderList(
            h,
            files.map((file) => {
              const path = `/settings/memory/${model.owner}/${model.name}/${file.file}`;
              return renderLinkRow(h, {
                key: file.file,
                path,
                mount: h.OnMount(RouterLink({ path })),
                title: h.span([], [fileTitle(file.file)]),
                children: [
                  h.span([h.Class("text-sm text-muted-foreground")], [lineText(file.lines)]),
                ],
              });
            }),
          ),
      }),
    ],
  );
}

function renderHunks(h: H, version: ShownVersion): Html {
  if (version.hunks.length === 0) {
    return h.p([h.Class("text-sm text-muted-foreground")], ["Only the final newline changed."]);
  }
  return h.div(
    [h.Class("grid gap-2")],
    version.hunks.flatMap((hunk, index) => [
      ...(index > 0
        ? [
            h.div([
              h.Key(`separator-${index}`),
              h.Role("separator"),
              h.Class("border-t border-dashed"),
            ]),
          ]
        : []),
      h.div([h.Key(`hunk-${index}`), h.Class(box)], [renderMarkdown(h, hunk, true)]),
    ]),
  );
}

function renderVersion(h: H, model: Model, version: ShownVersion): Html {
  return h.keyed("li")(
    String(version.id),
    [h.Class("grid gap-2")],
    [
      h.div(
        [h.Class("flex items-center justify-between gap-4")],
        [
          h.span(
            [h.Class("flex items-center gap-2")],
            [renderBadge(h, "secondary", version.author), dayClock(version.time)],
          ),
          renderButton(h, {
            label: "Revert",
            variant: "outline",
            pending: Option.exists(model.reverting, (id) => id === version.id),
            disabled: !version.revertible || Option.isSome(model.reverting),
            onClick: Message.ClickedRevert({ id: version.id }),
          }),
        ],
      ),
      version.reason ? h.p([h.Class("text-sm break-words")], [version.reason]) : h.empty,
      version.revertible
        ? h.empty
        : h.p([h.Class("text-sm text-muted-foreground")], [version.revert_problem]),
      renderHunks(h, version),
    ],
  );
}

function renderCurrent(h: H, model: Model): Html {
  const current = currentText(model);
  return h.section(
    [h.Class("grid gap-2")],
    [
      h.div(
        [h.Class("flex items-center justify-between gap-4")],
        [
          h.h3([h.Class("font-medium")], ["Current text"]),
          renderButton(h, { label: "Edit", variant: "outline", onClick: Message.ClickedEdit() }),
        ],
      ),
      h.div(
        [h.Class(box)],
        [current ? renderMarkdown(h, current) : h.span([], ["The memory file is empty."])],
      ),
    ],
  );
}

function renderHistory(h: H, model: Model): Html {
  const versions = versionsOf(model);
  return h.section(
    [h.Class("grid gap-2")],
    [
      h.h3([h.Class("font-medium")], ["History"]),
      versions.length === 0
        ? h.p([h.Class("text-muted-foreground")], ["The memory file has no version."])
        : h.empty,
      h.ul(
        [h.Class("grid gap-4")],
        versions.map((version) => renderVersion(h, model, version)),
      ),
      model.older
        ? renderButton(h, {
            label: "Show older versions",
            variant: "outline",
            pending: model.loadingOlder,
            onClick: Message.ClickedShowOlder(),
          })
        : h.empty,
    ],
  );
}

function renderEditDialog(h: H, model: Model, editing: Editing): Html {
  return renderDialog(h, {
    mount: h.OnMount(DialogOpened()),
    titleId: "memory-edit-title",
    title: `Edit ${pageTitle({ page: "file", owner: model.owner, name: model.name, file: model.file })}`,
    description: "",
    close: Message.RequestedClose(),
    className: "sm:max-w-2xl",
    body: h.div(
      [h.Class("grid min-h-0 grid-rows-[minmax(0,1fr)_auto] gap-2 px-4 pb-4")],
      [
        h.textarea([
          h.AriaLabel("Memory text"),
          h.DataAttribute("slot", "textarea"),
          h.DataAttribute("autofocus", ""),
          h.Value(editing.draft),
          h.OnInput((text) => Message.ChangedDraft({ text })),
          h.Class(textareaClass("h-full max-h-full min-h-40 resize-none font-mono")),
        ]),
        editing.error
          ? renderBadge(
              h,
              "destructive",
              editing.error,
              "h-auto w-full justify-start whitespace-normal",
            )
          : h.empty,
      ],
    ),
    footer: h.div(
      [
        h.DataAttribute("slot", "dialog-footer"),
        h.Class(
          "flex flex-col-reverse gap-2 rounded-b-xl border-t bg-muted/50 p-4 max-sm:rounded-none sm:flex-row sm:justify-end",
        ),
      ],
      [
        renderButton(h, {
          label: "Cancel",
          variant: "outline",
          disabled: editing.saving,
          onClick: Message.RequestedClose(),
        }),
        renderButton(h, {
          label: "Save",
          pending: editing.saving,
          onClick: Message.ClickedSave(),
        }),
      ],
    ),
  });
}

function renderDiscardDialog(h: H): Html {
  return renderDialog(h, {
    mount: h.OnMount(DialogOpened()),
    titleId: "memory-discard-title",
    title: "Discard your changes?",
    description: "The memory file keeps its text.",
    close: Message.ClickedKeepEditing(),
    layout: "center",
    footer: h.div(
      [
        h.DataAttribute("slot", "dialog-footer"),
        h.Class("flex flex-col-reverse gap-2 sm:flex-row sm:justify-end"),
      ],
      [
        renderButton(h, {
          label: "Keep editing",
          variant: "outline",
          autofocus: true,
          onClick: Message.ClickedKeepEditing(),
        }),
        renderButton(h, { label: "Discard", onClick: Message.ClickedDiscard() }),
      ],
    ),
  });
}

function renderFile(h: H, model: Model): Html {
  beginMarkdownRender();
  const title = pageTitle({ page: "file", owner: model.owner, name: model.name, file: model.file });
  return h.div(
    [h.Class("grid gap-6")],
    [
      renderPageHeader(h, title, Message.ClickedBack()),
      h.div(
        [h.Class(cn("grid gap-6", inset))],
        [
          model.error ? renderBadge(h, "destructive", model.error) : h.empty,
          Option.isSome(model.versions) ? renderCurrent(h, model) : h.empty,
          Option.isSome(model.versions) ? renderHistory(h, model) : h.empty,
        ],
      ),
      Option.match(model.editing, {
        onNone: () => h.empty,
        onSome: (editing) => renderEditDialog(h, model, editing),
      }),
      Option.exists(model.editing, (editing) => editing.discarding)
        ? renderDiscardDialog(h)
        : h.empty,
    ],
  );
}

export function view(model: Model, h: H): Html {
  switch (model.page) {
    case "repositories":
      return renderRepositories(h, model);
    case "files":
      return renderFiles(h, model);
    case "file":
      return renderFile(h, model);
  }
}
