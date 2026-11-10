import { cn } from "cn";
import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { buttonClass } from "@/ui/button";
import { spinnerIcon } from "@/ui/icons";
import { inputClass } from "@/ui/input";
import { inset, renderList, renderPageHeader, renderRow } from "@/ui/page";
import { Message } from "./message";
import { nameOf, type Model } from "./model";

type H = HtmlBuilder<Message>;

const labelClass =
  "group/field-label peer/field-label flex w-fit items-center gap-2 text-sm leading-snug font-medium select-none";

function renderAccountField(h: H, model: Model): Html {
  return h.div(
    [
      h.Role("group"),
      h.DataAttribute("slot", "field"),
      h.DataAttribute("orientation", "vertical"),
      h.Class("group/field flex w-full flex-col gap-2 *:w-full [&>.sr-only]:w-auto"),
    ],
    [
      h.label(
        [h.For("account"), h.DataAttribute("slot", "label"), h.Class(labelClass)],
        ["Account or organization"],
      ),
      h.input([
        h.Id("account"),
        h.DataAttribute("slot", "input"),
        h.Required(true),
        h.Value(model.account),
        h.OnInput((text) => Message.ChangedAccount({ text })),
        h.Class(inputClass()),
      ]),
    ],
  );
}

function renderNameField(h: H, model: Model): Html {
  const failed = Option.isSome(model.error);
  return h.div(
    [
      h.Role("group"),
      h.DataAttribute("slot", "field"),
      h.DataAttribute("orientation", "vertical"),
      ...(failed ? [h.DataAttribute("invalid", "true")] : []),
      h.Class(
        "group/field flex w-full flex-col gap-2 *:w-full data-[invalid=true]:text-destructive [&>.sr-only]:w-auto",
      ),
    ],
    [
      h.label([h.For("name"), h.DataAttribute("slot", "label"), h.Class(labelClass)], ["App name"]),
      h.input([
        h.Id("name"),
        h.DataAttribute("slot", "input"),
        h.Value(nameOf(model)),
        h.OnInput((text) => Message.ChangedName({ text })),
        h.AriaInvalid(failed),
        h.Class(inputClass()),
      ]),
      h.p(
        [
          h.DataAttribute("slot", "field-description"),
          h.Class(
            "text-left text-sm leading-normal font-normal text-muted-foreground last:mt-0 nth-last-2:-mt-1",
          ),
        ],
        [
          "GitHub App names are unique on all of GitHub. Use a name that no other App has, for example with your account name.",
        ],
      ),
      Option.match(model.error, {
        onNone: () => h.empty,
        onSome: (error) =>
          h.div(
            [
              h.Role("alert"),
              h.DataAttribute("slot", "field-error"),
              h.Class("text-sm font-normal text-destructive"),
            ],
            [error],
          ),
      }),
    ],
  );
}

function renderForm(h: H, model: Model): Html {
  return h.form(
    [h.OnSubmit(Message.SubmittedForm())],
    [
      h.div(
        [
          h.DataAttribute("slot", "field-group"),
          h.Class("group/field-group @container/field-group flex w-full flex-col gap-5"),
        ],
        [
          model.apps.length > 0 ? h.h3([h.Class("font-medium")], ["Add an organization"]) : h.empty,
          renderAccountField(h, model),
          renderNameField(h, model),
          h.button(
            [
              h.Type("submit"),
              h.DataAttribute("slot", "button"),
              h.Disabled(model.busy),
              h.Class(buttonClass({})),
            ],
            [model.busy ? spinnerIcon(h) : h.empty, "Create the App"],
          ),
        ],
      ),
    ],
  );
}

function renderSetup(h: H, model: Model): Html {
  return h.div(
    [
      h.DataAttribute("slot", "card"),
      h.Class(
        "flex w-full max-w-lg flex-col gap-4 overflow-hidden rounded-xl bg-card py-4 text-sm text-card-foreground ring-1 ring-foreground/10",
      ),
    ],
    [
      h.div(
        [
          h.DataAttribute("slot", "card-header"),
          h.Class("grid auto-rows-min items-start gap-1 rounded-t-xl px-4"),
        ],
        [
          h.div(
            [
              h.DataAttribute("slot", "card-title"),
              h.Class("font-heading text-base leading-snug font-medium"),
            ],
            ["Connect GitHub"],
          ),
        ],
      ),
      h.div([h.DataAttribute("slot", "card-content"), h.Class("px-4")], [renderForm(h, model)]),
    ],
  );
}

function renderApps(h: H, model: Model): Html {
  return renderList(
    h,
    model.apps.map((app) =>
      renderRow(h, app.slug, "grid gap-1 py-3", [
        h.a(
          [h.Attribute("href", app.installUrl), h.Class("font-medium hover:underline")],
          [`Install ${app.slug} on your repositories`],
        ),
        h.span(
          [h.Class("text-muted-foreground")],
          [app.repositories.length === 0 ? "No repositories" : app.repositories.join(", ")],
        ),
      ]),
    ),
  );
}

export function view(model: Model, h: H): Html {
  if (model.setup) {
    return renderSetup(h, model);
  }
  return h.div(
    [h.Class("grid gap-6")],
    [
      renderPageHeader(h, "Connect GitHub"),
      model.apps.length > 0
        ? h.p(
            [h.Class(cn("text-sm text-muted-foreground", inset))],
            ["Install each App on the repositories of its organization."],
          )
        : h.empty,
      renderApps(h, model),
      h.div([h.Class(cn("max-w-lg", inset))], [renderForm(h, model)]),
    ],
  );
}
