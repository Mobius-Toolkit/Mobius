import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { defineView } from "foldkit/submodel";
import { bindingText } from "@/lib/harness";
import { renderBadge } from "@/ui/badge";
import { buttonClass } from "@/ui/button";
import { checkIcon, chevronDownIcon, spinnerIcon } from "@/ui/icons";
import { defineInPlaceMount, renderSheet } from "@/ui/sheet";
import { Message } from "./message";
import { efforts, models, type Model } from "./model";
import type { Choice } from "./schemas";

type H = HtmlBuilder<Message>;

const SheetOpened = defineInPlaceMount(Message.CompletedFocusSheet);

const itemClass =
  "relative flex w-full cursor-default items-center gap-1.5 rounded-md py-1 pr-8 pl-1.5 text-left text-sm outline-hidden select-none hover:bg-accent hover:text-accent-foreground focus:bg-accent focus:text-accent-foreground [&_svg]:pointer-events-none [&_svg]:shrink-0";

function renderChoice(
  h: H,
  model: Model,
  options: {
    choice: Choice;
    id: string;
    value: string;
    values: readonly string[];
    pick: (value: string) => Message;
  },
): Html {
  const open = Option.exists(model.openChoice, (choice) => choice === options.choice);
  return h.div(
    [h.Class("relative flex min-w-0 grow")],
    [
      h.button(
        [
          h.Type("button"),
          h.Id(options.id),
          h.AriaHasPopup("menu"),
          h.AriaExpanded(open),
          h.DataAttribute("slot", "dropdown-menu-trigger"),
          h.Class(buttonClass({ variant: "outline", className: "min-w-0 grow justify-between" })),
          h.OnClick(Message.ToggledChoice({ choice: options.choice })),
        ],
        [h.span([h.Class("truncate")], [options.value]), chevronDownIcon(h)],
      ),
      open
        ? h.div([h.Class("fixed inset-0 z-40"), h.OnClick(Message.ClosedChoice())], [])
        : h.empty,
      open
        ? h.div(
            [
              h.Role("menu"),
              h.DataAttribute("slot", "dropdown-menu-content"),
              h.Class(
                "absolute bottom-full left-0 z-50 mb-1 max-h-60 w-full min-w-32 overflow-x-hidden overflow-y-auto rounded-lg bg-popover p-1 text-popover-foreground shadow-md ring-1 ring-foreground/10",
              ),
            ],
            Option.isNone(model.harnesses)
              ? [h.div([h.Class("flex justify-center p-2")], [spinnerIcon(h)])]
              : options.values.map((value) =>
                  h.keyed("button")(
                    value,
                    [
                      h.Type("button"),
                      h.Role("menuitemradio"),
                      h.AriaChecked(value === options.value),
                      h.Class(itemClass),
                      h.OnClick(options.pick(value)),
                    ],
                    [
                      value,
                      value === options.value
                        ? h.span(
                            [
                              h.Class(
                                "pointer-events-none absolute right-2 flex items-center justify-center",
                              ),
                            ],
                            [checkIcon(h, "size-4")],
                          )
                        : h.empty,
                    ],
                  ),
                ),
          )
        : h.empty,
    ],
  );
}

function renderField(h: H, label: string, id: string, child: Html): Html {
  return h.div(
    [h.Class("grid grid-cols-[4rem_1fr] items-center gap-2")],
    [
      h.label(
        [
          h.For(id),
          h.DataAttribute("slot", "label"),
          h.Class("flex items-center gap-2 text-sm leading-none font-medium select-none"),
        ],
        [label],
      ),
      h.div([h.Class("flex min-w-0 items-center gap-2")], [child]),
    ],
  );
}

export const view = defineView<Model, Message>((model, h) => {
  const harnessNames = Option.getOrElse(model.harnesses, () => []);
  const modelNames = models(model).map((other) => other.model);
  const effortNames = efforts(model);
  const choicesError = harnessNames.find((other) => other.harness === model.harness)?.error ?? "";
  return renderSheet(h, {
    mount: h.OnMount(SheetOpened()),
    titleId: "setting-title",
    descriptionId: "setting-description",
    title: model.title,
    description: `Default: ${bindingText(model.fallback)} (${model.fallbackSource})`,
    close: Message.ClickedClose(),
    className: "gap-3 p-4 pt-3 md:mx-auto md:max-w-md md:rounded-t-xl",
    children: [
      renderField(
        h,
        "Harness",
        "setting-harness",
        renderChoice(h, model, {
          choice: "harness",
          id: "setting-harness",
          value: model.harness,
          values: harnessNames.map((other) => other.harness),
          pick: (value) => Message.PickedHarness({ value }),
        }),
      ),
      renderField(
        h,
        "Model",
        "setting-model",
        renderChoice(h, model, {
          choice: "model",
          id: "setting-model",
          value: model.model,
          values: modelNames,
          pick: (value) => Message.PickedModel({ value }),
        }),
      ),
      effortNames.length > 0
        ? renderField(
            h,
            "Effort",
            "setting-effort",
            renderChoice(h, model, {
              choice: "effort",
              id: "setting-effort",
              value: model.effort,
              values: effortNames,
              pick: (value) => Message.PickedEffort({ value }),
            }),
          )
        : h.empty,
      choicesError
        ? renderBadge(h, "destructive", choicesError, "h-auto whitespace-normal")
        : h.empty,
      model.error
        ? renderBadge(h, "destructive", model.error, "h-auto whitespace-normal")
        : h.empty,
      h.div(
        [h.Class("flex items-center justify-between gap-2")],
        [
          Option.isSome(model.own)
            ? h.button(
                [
                  h.Type("button"),
                  h.Disabled(model.saving),
                  h.Class(buttonClass({ variant: "outline" })),
                  h.OnClick(Message.ClickedUseDefault()),
                ],
                ["Use the default"],
              )
            : h.span([]),
          h.button(
            [
              h.Type("button"),
              h.Disabled(Option.isNone(model.harnesses) || model.saving),
              h.Class(buttonClass({})),
              h.OnClick(Message.ClickedSave()),
            ],
            [model.saving ? spinnerIcon(h) : h.empty, "Save"],
          ),
        ],
      ),
    ],
  });
});
