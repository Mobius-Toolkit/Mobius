import { Option } from "effect";
import type { Return as UpdateReturn } from "foldkit/update";
import { CreateApp, RequestLogin } from "./commands";
import { Message } from "./message";
import { nameOf, type Model } from "./model";

export function update(model: Model, message: Message): UpdateReturn<Model, Message> {
  return Message.match<UpdateReturn<Model, Message>>(message, {
    ReceivedApps: ({ apps }) => ({ model: { ...model, apps } }),
    ChangedAccount: ({ text }) => ({ model: { ...model, account: text } }),
    ChangedName: ({ text }) => ({ model: { ...model, typedName: Option.some(text) } }),
    SubmittedForm: () => ({
      model: { ...model, busy: true },
      commands: [CreateApp({ account: model.account, name: nameOf(model) })],
    }),
    FormPosted: () => ({ model: { ...model, busy: false } }),
    CreateFailed: ({ error }) => ({
      model: { ...model, busy: false, error: Option.some(error) },
    }),
    LoginRequired: () => ({ model: { ...model, busy: false }, commands: [RequestLogin()] }),
    CompletedRequestLogin: () => ({ model }),
  });
}
