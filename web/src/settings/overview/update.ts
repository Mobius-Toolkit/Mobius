import { Option } from "effect";
import type { Return as UpdateReturn } from "foldkit/update";
import { Navigate, RequestLogin } from "./commands";
import { Message } from "./message";
import type { Model } from "./model";

export function update(model: Model, message: Message): UpdateReturn<Model, Message> {
  return Message.match<UpdateReturn<Model, Message>>(message, {
    ReceivedSwitches: ({ switches }) => ({
      model: { ...model, switches: Option.fromNullishOr(switches) },
    }),
    ReceivedErrors: ({ errors }) => ({ model: { ...model, errors } }),
    VersionLoaded: ({ release }) => ({ model: { ...model, server: release } }),
    VoiceLoaded: ({ voice }) => ({ model: { ...model, voice: Option.some(voice) } }),
    LoginRequired: () => ({ model, commands: [RequestLogin()] }),
    ReadFailed: () => ({ model }),
    NavigateTo: ({ path }) => ({ model, commands: [Navigate({ path })] }),
    CompletedRequestLogin: () => ({ model }),
    CompletedNavigate: () => ({ model }),
  });
}
