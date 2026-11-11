import * as Subscription from "foldkit/subscription";
import { subscriptions as transcriptSubscriptions } from "@/transcript/subscriptions";
import { Message, toGotTranscriptMessage } from "./message";
import type { Model } from "./model";

export const subscriptions = Subscription.lift(transcriptSubscriptions)<Model, Message>({
  read: (model) => model.transcript,
  toParentMessage: toGotTranscriptMessage,
});
