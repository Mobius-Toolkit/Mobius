import type { Return as UpdateReturn } from "foldkit/update";
import { Navigate } from "./commands";
import { Message } from "./message";
import type { Model } from "./model";

export function update(model: Model, message: Message): UpdateReturn<Model, Message> {
  return Message.match<UpdateReturn<Model, Message>>(message, {
    ReceivedWorkstreams: ({ workstreams, working, error }) => ({
      model: { ...model, workstreams, working, error },
    }),
    ReceivedUnread: ({ unread }) => ({ model: { ...model, unread } }),
    ReceivedNeedsHuman: ({ needsHuman }) => ({ model: { ...model, needsHuman } }),
    NavigateTo: ({ path }) => ({ model, commands: [Navigate({ path })] }),
    CompletedNavigate: () => ({ model }),
  });
}
