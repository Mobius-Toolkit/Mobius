import { Option } from "effect";
import * as Update from "foldkit/update";
import { lowLoadReason } from "@/lib/agents";
import { OutMessage as TranscriptOutMessage } from "@/transcript/message";
import { initialModel as initialTranscript } from "@/transcript/model";
import { reflectStartable, update as updateTranscript } from "@/transcript/update";
import { FetchAgents } from "./commands";
import { Message, OutMessage, toGotTranscriptMessage } from "./message";
import type { Model } from "./model";

type UpdateReturn = Update.ReturnWithOutMessage<Model, Message, OutMessage>;

export const reload = (model: Model) =>
  FetchAgents({ owner: model.owner, name: model.name, number: model.number });

export const refresh = (model: Model): Update.Return<Model, Message> => ({
  model,
  commands: [reload(model)],
});

const foldTranscript = Update.foldChild({
  update: updateTranscript,
  read: (model: Model) => model.transcript,
  write: (model, transcript) => ({ ...model, transcript: Option.some(transcript) }),
  toParentMessage: toGotTranscriptMessage,
  foldOutMessage: (outMessage) =>
    TranscriptOutMessage.match<Update.StepWithOutMessage<Model, Message, OutMessage>>(outMessage, {
      Closed: () => (model) => ({ model: { ...model, transcript: Option.none() } }),
      LoginRequired: () => (model) => ({ model, outMessage: OutMessage.LoginRequired() }),
    }),
});

const foldStartable = Update.foldChild({
  update: reflectStartable,
  read: (model: Model) => model.transcript,
  write: (model, transcript) => ({ ...model, transcript: Option.some(transcript) }),
  toParentMessage: toGotTranscriptMessage,
});

// The list shows the live state of the open agent, and keeps the state that the Owner opened when the agent leaves the
// list.
function keepStartable(model: Model): UpdateReturn {
  const open = Option.getOrUndefined(model.transcript);
  const live = open && model.agents.find((agent) => agent.id === open.agent.id);
  return live ? foldStartable(model, live.queueReason === lowLoadReason) : { model };
}

export function update(model: Model, message: Message): UpdateReturn {
  return Message.match<UpdateReturn>(message, {
    AgentsLoaded: ({ agents }) => keepStartable({ ...model, agents }),
    LoadFailed: ({ error }) => ({ model: { ...model, error } }),
    LoginRequired: () => ({ model, outMessage: OutMessage.LoginRequired() }),
    ToggledStopped: () => ({ model: { ...model, showStopped: !model.showStopped } }),
    OpenedAgent: ({ agent }) => ({
      model: { ...model, transcript: Option.some(initialTranscript(agent)) },
    }),
    GotTranscriptMessage: ({ message: child }) => foldTranscript(model, child),
  });
}
