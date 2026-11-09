import { Option } from "effect";
import type { Return, ReturnWithOutMessage } from "foldkit/update";
import { keepEffort } from "@/lib/harness";
import { ClearSetting, FetchHarnesses, SaveSetting } from "./commands";
import { Message, OutMessage } from "./message";
import { efforts, initialModel, models, type Model } from "./model";

type UpdateReturn = ReturnWithOutMessage<Model, Message, OutMessage>;

export function init(options: Parameters<typeof initialModel>[0]): Return<Model, Message> {
  return { model: initialModel(options), commands: [FetchHarnesses()] };
}

const closed = (model: Model): Model => ({ ...model, openChoice: Option.none() });

function pickHarness(model: Model, value: string): Model {
  if (value === model.harness) {
    return closed(model);
  }
  const first = Option.getOrElse(model.harnesses, () => []).find((other) => other.harness === value)
    ?.models[0];
  return {
    ...closed(model),
    harness: value,
    model: first?.model ?? "",
    effort: keepEffort(model.effort, first?.efforts ?? []),
  };
}

function pickModel(model: Model, value: string): Model {
  const chosen = models(model).find((other) => other.model === value);
  return {
    ...closed(model),
    model: value,
    effort: keepEffort(model.effort, chosen?.efforts ?? []),
  };
}

export function update(model: Model, message: Message): UpdateReturn {
  return Message.match<UpdateReturn>(message, {
    HarnessesLoaded: ({ harnesses }) => ({
      model: { ...model, harnesses: Option.some(harnesses) },
    }),
    HarnessesFailed: ({ error }) => ({
      model: { ...model, harnesses: Option.some([]), error },
    }),
    LoginRequired: () => ({
      model: { ...model, saving: false },
      outMessage: OutMessage.LoginRequired(),
    }),
    PickedHarness: ({ value }) => ({ model: pickHarness(model, value) }),
    PickedModel: ({ value }) => ({ model: pickModel(model, value) }),
    PickedEffort: ({ value }) => ({ model: { ...closed(model), effort: value } }),
    ToggledChoice: ({ choice }) => ({
      model: {
        ...model,
        openChoice: Option.isSome(model.openChoice) ? Option.none() : Option.some(choice),
      },
    }),
    ClosedChoice: () => ({ model: closed(model) }),
    PressedEscape: () =>
      Option.isSome(model.openChoice)
        ? { model: closed(model) }
        : { model, outMessage: OutMessage.Closed() },
    ClickedSave: () => ({
      model: { ...model, saving: true, error: "" },
      commands: [
        SaveSetting({
          target: model.target,
          title: model.title,
          setting: {
            harness: model.harness,
            model: model.model,
            effort: efforts(model).length > 0 ? model.effort : "",
          },
        }),
      ],
    }),
    ClickedUseDefault: () => ({
      model: { ...model, saving: true, error: "" },
      commands: [ClearSetting({ target: model.target, title: model.title })],
    }),
    Saved: ({ setting }) => ({
      model,
      outMessage: OutMessage.Changed({ setting: Option.some(setting) }),
    }),
    Cleared: () => ({ model, outMessage: OutMessage.Changed({ setting: Option.none() }) }),
    Failed: ({ error }) => ({ model: { ...model, saving: false, error } }),
    ClickedClose: () => ({ model, outMessage: OutMessage.Closed() }),
    CompletedFocusSheet: () => ({ model }),
  });
}
