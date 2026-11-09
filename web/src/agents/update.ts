import { Option } from "effect";
import { Command } from "foldkit";
import * as Update from "foldkit/update";
import { lowLoadReason } from "@/lib/agents";
import { FetchLog } from "@/transcript/commands";
import { OutMessage as TranscriptOutMessage } from "@/transcript/message";
import { initialModel as initialTranscript } from "@/transcript/model";
import { reflectStartable, update as updateTranscript } from "@/transcript/update";
import { FetchAgents, ReportTitle, RequestLogin } from "./commands";
import { Message, toGotTranscriptMessage } from "./message";
import { titleOf, type Model } from "./model";

type UpdateReturn = Update.Return<Model, Message>;

const close: Update.Step<Model, Message> = (model) => ({
  model: { ...model, transcript: Option.none() },
  commands: [ReportTitle({ title: "" })],
});

const foldTranscript = Update.foldChild({
  update: updateTranscript,
  read: (model: Model) => model.transcript,
  write: (model, transcript) => ({ ...model, transcript: Option.some(transcript) }),
  toParentMessage: toGotTranscriptMessage,
  foldOutMessage: (outMessage) =>
    TranscriptOutMessage.match<Update.Step<Model, Message>>(outMessage, {
      Closed: () => close,
      LoginRequired: () => (model) => ({ model, commands: [RequestLogin()] }),
    }),
});

const reload = (model: Model): UpdateReturn => ({
  model: { ...model, request: model.request + 1 },
  commands: [FetchAgents({ request: model.request + 1 })],
});

const foldStartable = Update.foldChild({
  update: reflectStartable,
  read: (model: Model) => model.transcript,
  write: (model, transcript) => ({ ...model, transcript: Option.some(transcript) }),
  toParentMessage: toGotTranscriptMessage,
});

// The start button of the open agent follows the list. It goes away when the agent leaves the list.
function followList(model: Model): UpdateReturn {
  if (Option.isNone(model.transcript) || Option.isNone(model.agents)) {
    return { model };
  }
  const id = model.transcript.value.agent.id;
  const live = model.agents.value.groups
    .flatMap((group) => group.agents)
    .find((row) => row.agent.id === id);
  return foldStartable(model, live?.agent.queueReason === lowLoadReason);
}

export function update(model: Model, message: Message): UpdateReturn {
  return Message.match<UpdateReturn>(message, {
    ConnectionOpened: () => reload(model),
    AgentChanged: () => reload(model),
    LoginRequired: () => ({ model, commands: [RequestLogin()] }),
    AgentsLoaded: ({ request, agents }) =>
      request === model.request ? followList({ ...model, agents: Option.some(agents) }) : { model },
    LoadFailed: ({ request, error }) =>
      request === model.request ? { model: { ...model, error } } : { model },
    OpenedAgent: ({ agent }) => {
      const transcript = initialTranscript(agent);
      return {
        model: { ...model, transcript: Option.some(transcript) },
        commands: [
          ReportTitle({ title: titleOf(transcript) }),
          ...Command.mapMessages(
            [FetchLog({ id: agent.id, known: Option.none() })],
            toGotTranscriptMessage,
          ),
        ],
      };
    },
    ClickedBack: () => close(model),
    BackRequested: () => close(model),
    GotTranscriptMessage: ({ message: child }) => foldTranscript(model, child),
    CompletedRequestLogin: () => ({ model }),
    CompletedReportTitle: () => ({ model }),
  });
}
