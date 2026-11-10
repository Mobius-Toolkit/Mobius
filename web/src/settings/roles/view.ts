import { cn } from "cn";
import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { renderBadge } from "@/ui/badge";
import { buttonClass } from "@/ui/button";
import { renderChoice as renderChoiceMenu, renderField } from "@/ui/choice";
import { chevronRightIcon, spinnerIcon } from "@/ui/icons";
import { inputClass } from "@/ui/input";
import { renderErrorBadge, renderList, renderPageHeader, rowClass } from "@/ui/page";
import { defineInPlaceMount, renderSheet } from "@/ui/sheet";
import { Message } from "./message";
import {
  effortsOf,
  formatContext,
  hasContextLimit,
  harnessNames,
  modelsOf,
  roleDescriptions,
  roleName,
  sheetChanged,
  type Model,
  type Sheet,
} from "./model";
import type { Choice, Role } from "./schemas";

type H = HtmlBuilder<Message>;

const SheetOpened = defineInPlaceMount(Message.CompletedFocusSheet);

const uiBadge = (h: H) => renderBadge(h, "secondary", "UI", "shrink-0");

function renderRoleRow(h: H, role: Role): Html {
  const values = [
    { text: role.harness, ui: role.ui.harness },
    { text: role.model, ui: role.ui.model },
    ...(role.effort ? [{ text: role.effort, ui: role.ui.effort }] : []),
    ...(role.context > 0 ? [{ text: formatContext(role.context), ui: role.ui.context }] : []),
    { text: `max ${role.max}`, ui: role.ui.max },
  ];
  return h.keyed("li")(
    role.role,
    [],
    [
      h.button(
        [
          h.Type("button"),
          h.Class(cn(rowClass, "w-full text-left hover:bg-muted")),
          h.OnClick(Message.OpenedRole({ role: role.role })),
        ],
        [
          h.span(
            [h.Class("grid min-w-0 grow gap-0.5")],
            [
              h.span([], [roleName(role)]),
              h.span(
                [
                  h.Class(
                    "flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-sm text-muted-foreground",
                  ),
                ],
                values.flatMap((value, index) => [
                  ...(index > 0 ? [h.span([h.AriaHidden(true)], ["·"])] : []),
                  h.span([h.Class("break-all")], [value.text]),
                  ...(value.ui ? [uiBadge(h)] : []),
                ]),
              ),
            ],
          ),
          chevronRightIcon(h, "size-4 shrink-0"),
        ],
      ),
    ],
  );
}

function renderChoice(
  h: H,
  model: Model,
  sheet: Sheet,
  options: {
    choice: Choice;
    id: string;
    value: string;
    values: readonly string[];
    pick: (value: string) => Message;
  },
): Html {
  return renderChoiceMenu(h, {
    id: options.id,
    value: options.value,
    values: options.values,
    loading: Option.isNone(model.harnesses),
    open: Option.exists(sheet.openChoice, (choice) => choice === options.choice),
    side: "below",
    toggle: Message.ToggledChoice({ choice: options.choice }),
    close: Message.ClosedChoice(),
    pick: options.pick,
  });
}

function renderChoiceField(
  h: H,
  model: Model,
  sheet: Sheet,
  options: {
    label: string;
    choice: Choice;
    value: string;
    values: readonly string[];
    ui: boolean;
    pick: (value: string) => Message;
  },
): Html {
  const id = `role-${options.choice}`;
  return renderField(h, options.label, id, [
    renderChoice(h, model, sheet, { ...options, id }),
    options.ui ? uiBadge(h) : h.empty,
  ]);
}

function renderSheetOf(h: H, model: Model, sheet: Sheet): Html {
  const efforts = effortsOf(model, sheet);
  const supportsContext = hasContextLimit(sheet.harness);
  const choicesError =
    Option.getOrElse(model.harnesses, () => []).find((other) => other.harness === sheet.harness)
      ?.error ?? "";
  const role = sheet.role;
  return renderSheet(h, {
    mount: h.OnMount(SheetOpened()),
    titleId: "role-title",
    title: roleName(role),
    description: { id: "role-description", text: roleDescriptions[role.role] ?? "" },
    close: Message.ClosedSheet(),
    className: "gap-3 p-4 pt-3 md:mx-auto md:max-w-md md:rounded-t-xl",
    children: [
      renderChoiceField(h, model, sheet, {
        label: "Harness",
        choice: "harness",
        value: sheet.harness,
        values: harnessNames(model),
        ui: role.ui.harness,
        pick: (value) => Message.PickedHarness({ value }),
      }),
      renderChoiceField(h, model, sheet, {
        label: "Model",
        choice: "model",
        value: sheet.model,
        values: modelsOf(model, sheet.harness).map((other) => other.model),
        ui: role.ui.model,
        pick: (value) => Message.PickedModel({ value }),
      }),
      efforts.length > 0
        ? renderChoiceField(h, model, sheet, {
            label: "Effort",
            choice: "effort",
            value: sheet.effort,
            values: efforts,
            ui: role.ui.effort,
            pick: (value) => Message.PickedEffort({ value }),
          })
        : h.empty,
      renderField(h, "Max", "role-max", [
        h.input([
          h.Id("role-max"),
          h.Type("number"),
          h.Min("1"),
          h.Value(sheet.max),
          h.OnInput((text) => Message.ChangedMax({ text })),
          h.Class(inputClass()),
        ]),
        role.ui.max ? uiBadge(h) : h.empty,
      ]),
      renderField(h, "Context", "role-context", [
        h.input([
          h.Id("role-context"),
          h.Type("number"),
          h.Min("100000"),
          h.Max("1000000"),
          h.Step("1000"),
          h.Placeholder("No limit"),
          h.Value(sheet.context),
          h.Disabled(!supportsContext),
          h.OnInput((text) => Message.ChangedContext({ text })),
          h.Class(inputClass("min-w-0 grow")),
        ]),
        h.span([h.Class("shrink-0 text-sm text-muted-foreground")], ["tokens"]),
        role.ui.context ? uiBadge(h) : h.empty,
      ]),
      supportsContext
        ? h.empty
        : h.p([h.Class("text-sm text-muted-foreground")], ["This Harness has no context limit."]),
      choicesError
        ? renderBadge(h, "destructive", choicesError, "h-auto whitespace-normal")
        : h.empty,
      sheet.error
        ? renderBadge(h, "destructive", sheet.error, "h-auto whitespace-normal")
        : h.empty,
      h.div(
        [h.Class("flex items-center justify-between gap-2")],
        [
          sheetChanged(sheet)
            ? h.button(
                [
                  h.Type("button"),
                  h.Disabled(sheet.saving),
                  h.Class(buttonClass({ variant: "outline" })),
                  h.OnClick(Message.ClickedReset()),
                ],
                ["Reset"],
              )
            : h.span([]),
          h.button(
            [
              h.Type("button"),
              h.Disabled(Option.isNone(model.harnesses) || sheet.saving),
              h.Class(buttonClass({})),
              h.OnClick(Message.ClickedSave()),
            ],
            [sheet.saving ? spinnerIcon(h) : h.empty, "Save"],
          ),
        ],
      ),
    ],
  });
}

export function view(model: Model, h: H): Html {
  return h.div(
    [h.Class("grid gap-6")],
    [
      renderPageHeader(h, "Roles", Message.ClickedBack()),
      model.error ? renderErrorBadge(h, model.error) : h.empty,
      renderList(
        h,
        Option.getOrElse(model.roles, () => []).map((role) => renderRoleRow(h, role)),
      ),
      Option.match(model.sheet, {
        onNone: () => h.empty,
        onSome: (sheet) => renderSheetOf(h, model, sheet),
      }),
    ],
  );
}
