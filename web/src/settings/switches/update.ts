import { Option } from "effect";
import type { Return as UpdateReturn } from "foldkit/update";
import { Message } from "./message";
import type { Model } from "./model";

export function update(model: Model, message: Message): UpdateReturn<Model, Message> {
  return Message.match<UpdateReturn<Model, Message>>(message, {
    ReceivedSwitches: ({ switches }) => ({
      model: { ...model, switches: Option.fromNullishOr(switches) },
    }),
    ReceivedErrors: ({ errors }) => ({ model: { ...model, errors } }),
    ClickedSwitch: () => ({ model }),
    ClickedBack: () => ({ model }),
  });
}
