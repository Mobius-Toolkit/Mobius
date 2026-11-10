import { Option } from "effect";
import type { Return as UpdateReturn } from "foldkit/update";
import { FetchDevices, LogOut, RequestLogin } from "./commands";
import { Message } from "./message";
import type { Model } from "./model";

const without = (model: Model, id: number) => model.loggingOut.filter((other) => other !== id);

export function update(model: Model, message: Message): UpdateReturn<Model, Message> {
  return Message.match<UpdateReturn<Model, Message>>(message, {
    DevicesLoaded: ({ devices }) => ({ model: { ...model, devices: Option.some(devices) } }),
    LoadFailed: ({ error }) => ({ model: { ...model, error } }),
    LoginRequired: () => ({ model, commands: [RequestLogin()] }),
    ClickedLogOut: ({ id }) => ({
      model: { ...model, loggingOut: [...model.loggingOut, id] },
      commands: [LogOut({ id })],
    }),
    // The row keeps its spinner until the list no longer has the device.
    LoggedOut: () => ({ model, commands: [FetchDevices()] }),
    LogOutFailed: ({ id, error }) => ({
      model: { ...model, error, loggingOut: without(model, id) },
    }),
    LogOutLoginRequired: ({ id }) => ({
      model: { ...model, loggingOut: without(model, id) },
      commands: [RequestLogin()],
    }),
    CompletedRequestLogin: () => ({ model }),
  });
}
