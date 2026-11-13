import { Schema } from "effect";
import type { DraftVoice } from "@/lib/draft";
import {
  type DraftInboxItem,
  DraftInboxItems,
  type DraftSelection,
  DraftSelections,
  DraftVoice as DraftVoiceSchema,
  Images,
  VoiceState,
} from "@/chat/schemas";

export const inputId = (instance: string) => `${instance}-input`;
export const pickerId = (instance: string) => `${instance}-attach`;

export const isEmptyDraft = (model: {
  draftText: string;
  draftImages: readonly File[];
  draftInboxItems: readonly DraftInboxItem[];
  draftSelections: readonly DraftSelection[];
}) =>
  !model.draftText.trim() &&
  model.draftImages.length === 0 &&
  model.draftInboxItems.length === 0 &&
  model.draftSelections.length === 0;

// A card that converts or has failed blocks each send.
export const voiceBusy = (voice: DraftVoice | null) =>
  voice?.state === "converting" || voice?.state === "failed";

export const Model = Schema.Struct({
  draftKey: Schema.String,
  instance: Schema.String,
  label: Schema.String,
  placeholder: Schema.String,
  organization: Schema.String,
  repository: Schema.String,
  workstream: Schema.Number,
  draftText: Schema.String,
  draftImages: Images,
  draftInboxItems: DraftInboxItems,
  draftSelections: DraftSelections,
  draftVoice: Schema.NullOr(DraftVoiceSchema),
  draftLoaded: Schema.Boolean,
  voice: VoiceState,
  now: Schema.Number,
  sendError: Schema.String,
});

export type Model = Schema.Schema.Type<typeof Model>;

export function initialModel(options: {
  draftKey: string;
  instance: string;
  label: string;
  placeholder: string;
  organization: string;
  repository: string;
  workstream: number;
}): Model {
  return {
    ...options,
    draftText: "",
    draftImages: [],
    draftInboxItems: [],
    draftSelections: [],
    draftVoice: null,
    draftLoaded: false,
    voice: { recording: false, key: "", startedAt: 0, live: "", notice: { key: "", text: "" } },
    now: 0,
    sendError: "",
  };
}
