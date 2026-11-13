import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import {
  Draft,
  DraftInboxItem,
  DraftInboxItemId,
  Images,
  Queued,
  VoiceState,
} from "@/chat/schemas";

export const Message = defineMessageUnion({
  ReceivedDraft: { draft: Draft },
  DraftTextChanged: { text: Schema.String },
  ImagesChosen: { files: Images },
  ImagesFitted: { images: Images, problem: Schema.String },
  AttachClicked: {},
  CompletedClickAttach: {},
  DraftImageRemoved: { position: Schema.Number },
  DraftInboxItemAdded: { item: DraftInboxItem },
  DraftInboxItemRemoved: { id: DraftInboxItemId },
  SendSubmitted: {},
  StopClicked: {},
  CompletedSaveDraft: {},
  VoiceButtonClicked: {},
  VoiceResendClicked: {},
  VoiceRemoveClicked: {},
  VoiceChanged: { voice: VoiceState },
  VoiceTicked: { now: Schema.Number },
  CompletedToggleVoice: {},
  CompletedStopVoice: {},
  CompletedResendVoice: {},
  CompletedRemoveVoice: {},
  CompletedScrollLive: {},
  CompletedShowFile: {},
  MessageQueued: { item: Queued },
  MessageRefused: { error: Schema.String },
});

export type Message = typeof Message.Type;

export const OutMessage = defineMessageUnion({
  StopClicked: {},
  MessageEnqueued: { item: Queued },
});

export type OutMessage = typeof OutMessage.Type;
