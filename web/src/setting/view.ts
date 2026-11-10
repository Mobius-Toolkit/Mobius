import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { defineView } from "foldkit/submodel";
import { bindingText } from "@/lib/harness";
import { renderBadge } from "@/ui/badge";
import { buttonClass } from "@/ui/button";
import { renderChoice as renderChoiceMenu, renderField } from "@/ui/choice";
import { spinnerIcon } from "@/ui/icons";
import { defineInPlaceMount, renderSheet } from "@/ui/sheet";
import { Message } from "./message";
import { efforts, models, type Model } from "./model";
import type { Choice } from "./schemas";

type H = HtmlBuilder<Message>;

const SheetOpened = defineInPlaceMount(Message.CompletedFocusSheet);

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
  return renderChoiceMenu(h, {
    id: options.id,
    value: options.value,
    values: options.values,
    loading: Option.isNone(model.harnesses),
    open: Option.exists(model.openChoice, (choice) => choice === options.choice),
    side: "above",
    toggle: Message.ToggledChoice({ choice: options.choice }),
    close: Message.ClosedChoice(),
    pick: options.pick,
  });
}

export const view = defineView<Model, Message>((model, h) => {
  const harnessNames = Option.getOrElse(model.harnesses, () => []);
  const modelNames = models(model).map((other) => other.model);
  const effortNames = efforts(model);
  const choicesError = harnessNames.find((other) => other.harness === model.harness)?.error ?? "";
  return renderSheet(h, {
    mount: h.OnMount(SheetOpened()),
    titleId: "setting-title",
    title: model.title,
    description: {
      id: "setting-description",
      text: `Default: ${bindingText(model.fallback)} (${model.fallbackSource})`,
    },
    close: Message.ClickedClose(),
    className: "gap-3 p-4 pt-3 md:mx-auto md:max-w-md md:rounded-t-xl",
    children: [
      renderField(h, "Harness", "setting-harness", [
        renderChoice(h, model, {
          choice: "harness",
          id: "setting-harness",
          value: model.harness,
          values: harnessNames.map((other) => other.harness),
          pick: (value) => Message.PickedHarness({ value }),
        }),
      ]),
      renderField(h, "Model", "setting-model", [
        renderChoice(h, model, {
          choice: "model",
          id: "setting-model",
          value: model.model,
          values: modelNames,
          pick: (value) => Message.PickedModel({ value }),
        }),
      ]),
      effortNames.length > 0
        ? renderField(h, "Effort", "setting-effort", [
            renderChoice(h, model, {
              choice: "effort",
              id: "setting-effort",
              value: model.effort,
              values: effortNames,
              pick: (value) => Message.PickedEffort({ value }),
            }),
          ])
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
