import { Schema } from "effect";
import type { DraftVoice } from "@/lib/draft";
import {
  type DraftInboxItem,
  DraftInboxItems,
  DraftVoice as DraftVoiceSchema,
  Images,
  VoiceState,
} from "@/chat/schemas";

export const inputId = (instance: string) => `${instance}-input`;
export const pickerId = (instance: string) => `${instance}-attach`;

export function parseChatDraftKey(key: string) {
  if (!key.startsWith("chatDraft:")) {
    return { organization: "", repository: "", workstream: 0 };
  }
  const raw = key.slice("chatDraft:".length);
  const firstSlash = raw.indexOf("/");
  const lastSlash = raw.lastIndexOf("/");
  if (firstSlash === -1 || lastSlash === -1) {
    return { organization: "", repository: "", workstream: 0 };
  }
  const organization = raw.slice(0, firstSlash);
  const repository = raw.slice(firstSlash + 1, lastSlash);
  const workstream = Number(raw.slice(lastSlash + 1)) || 0;
  return { organization, repository, workstream };
}

export const isEmptyDraft = (model: {
  draftText: string;
  draftImages: readonly File[];
  draftInboxItems: readonly DraftInboxItem[];
}) =>
  !model.draftText.trim() && model.draftImages.length === 0 && model.draftInboxItems.length === 0;

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
  organization?: string;
  repository?: string;
  workstream?: number;
}): Model {
  const chat =
    options.organization !== undefined &&
    options.repository !== undefined &&
    options.workstream !== undefined
      ? {
          organization: options.organization,
          repository: options.repository,
          workstream: options.workstream,
        }
      : parseChatDraftKey(options.draftKey);

  return {
    draftKey: options.draftKey,
    instance: options.instance,
    label: options.label,
    placeholder: options.placeholder,
    organization: chat.organization,
    repository: chat.repository,
    workstream: chat.workstream,
    draftText: "",
    draftImages: [],
    draftInboxItems: [],
    draftVoice: null,
    draftLoaded: false,
    voice: { recording: false, key: "", startedAt: 0, live: "", notice: { key: "", text: "" } },
    now: 0,
    sendError: "",
  };
}
