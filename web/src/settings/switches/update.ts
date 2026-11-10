import { Option } from "effect";
import type { Return as UpdateReturn } from "foldkit/update";
import { GoBack, RequestSwitch } from "./commands";
import { Message } from "./message";
import type { Model } from "./model";

export function update(model: Model, message: Message): UpdateReturn<Model, Message> {
  return Message.match<UpdateReturn<Model, Message>>(message, {
    ReceivedSwitches: ({ switches }) => ({
      model: { ...model, switches: Option.fromNullishOr(switches) },
    }),
    ReceivedErrors: ({ errors }) => ({ model: { ...model, errors } }),
    ClickedSwitch: ({ kind, on }) => ({ model, commands: [RequestSwitch({ kind, on })] }),
    ClickedBack: () => ({ model, commands: [GoBack()] }),
    CompletedRequestSwitch: () => ({ model }),
    CompletedGoBack: () => ({ model }),
  });
}
