import * as Subscription from "foldkit/subscription";
import { subscriptions as transcriptSubscriptions } from "@/transcript/subscriptions";
import { toGotTranscriptMessage } from "./message";
import type { Message } from "./message";
import type { Model } from "./model";

const transcript = Subscription.lift(transcriptSubscriptions)<Model, Message>({
  read: (model) => model.transcript,
  toParentMessage: toGotTranscriptMessage,
});

export const subscriptions = { transcriptStream: transcript.stream };
