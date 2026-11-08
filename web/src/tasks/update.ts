import { Option } from "effect";
import type { Return as UpdateReturn } from "foldkit/update";
import { FetchBoard, Navigate, RequestLogin } from "./commands";
import { Message } from "./message";
import type { Model } from "./model";

export function update(model: Model, message: Message): UpdateReturn<Model, Message> {
  return Message.match<UpdateReturn<Model, Message>>(message, {
    ConnectionOpened: () => ({ model, commands: [FetchBoard()] }),
    BoardChanged: () => ({ model, commands: [FetchBoard()] }),
    LoginRequired: () => ({ model, commands: [RequestLogin()] }),
    BoardLoaded: ({ tasks }) => ({ model: { ...model, tasks: Option.some(tasks), error: "" } }),
    BoardFailed: ({ error }) => ({ model: { ...model, error } }),
    Ticked: ({ now }) => ({ model: { ...model, now } }),
    NavigateTo: ({ path }) => ({ model, commands: [Navigate({ path })] }),
    CompletedNavigate: () => ({ model }),
    CompletedRequestLogin: () => ({ model }),
  });
}
