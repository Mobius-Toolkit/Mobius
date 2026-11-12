import type * as Update from "foldkit/update";
import type { DraftInboxItem } from "@/chat/schemas";
import { maxImages, tooManyImages } from "@/lib/images";
import {
  ClickAttach,
  EnqueueMessage,
  FitImages,
  RemoveVoice,
  ResendVoice,
  SaveDraftImages,
  SaveDraftInboxItems,
  SaveDraftText,
  StopVoice,
  ToggleVoice,
} from "./commands";
import { Message, OutMessage } from "./message";
import { initialModel, inputId, isEmptyDraft, type Model, pickerId, voiceBusy } from "./model";

type UpdateReturn = Update.ReturnWithOutMessage<Model, Message, OutMessage>;

export function init(options: Parameters<typeof initialModel>[0]): Update.Return<Model, Message> {
  return {
    model: initialModel(options),
    commands: [],
  };
}

function clearedDraft(model: Model): Model {
  return {
    ...model,
    draftText: "",
    draftImages: [],
    draftInboxItems: [],
    draftVoice: null,
  };
}

export function update(model: Model, message: Message): UpdateReturn {
  return Message.match<UpdateReturn>(message, {
    ReceivedDraft: ({ draft }) => ({
      model: {
        ...model,
        draftLoaded: true,
        draftText: draft.text,
        draftImages: draft.images,
        draftInboxItems: draft.inboxItems,
        draftVoice: draft.voice,
      },
    }),

    DraftTextChanged: ({ text }) => ({
      model: { ...model, draftText: text },
      commands: [SaveDraftText({ key: model.draftKey, text })],
    }),

    ImagesChosen: ({ files }) => ({
      model,
      commands: [
        FitImages({
          files,
          room: maxImages - model.draftImages.length,
        }),
      ],
    }),

    ImagesFitted: ({ images, problem }) => {
      const all = [...model.draftImages, ...images];
      const nextImages = all.slice(0, maxImages);
      return {
        model: {
          ...model,
          draftImages: nextImages,
          sendError: !problem && all.length > maxImages ? tooManyImages : problem,
        },
        commands: [SaveDraftImages({ key: model.draftKey, images: nextImages })],
      };
    },

    AttachClicked: () => ({
      model,
      commands: [ClickAttach({ pickerId: pickerId(model.instance) })],
    }),

    CompletedClickAttach: () => ({ model }),

    DraftImageRemoved: ({ position }) => {
      const nextImages = model.draftImages.filter((_, i) => i !== position);
      return {
        model: { ...model, draftImages: nextImages },
        commands: [SaveDraftImages({ key: model.draftKey, images: nextImages })],
      };
    },

    DraftInboxItemAdded: ({ item }) => addDraftInboxItem(model, item),

    DraftInboxItemRemoved: ({ id }) => {
      const nextItems = model.draftInboxItems.filter((item) => item.id !== id);
      return {
        model: { ...model, draftInboxItems: nextItems },
        commands: [SaveDraftInboxItems({ key: model.draftKey, inboxItems: nextItems })],
      };
    },

    // The Send button and Enter stop a voice run and send nothing.
    SendSubmitted: () => {
      if (model.voice.recording) {
        return { model, commands: [StopVoice()] };
      }
      if (isEmptyDraft(model) || voiceBusy(model.draftVoice)) {
        return { model };
      }
      return {
        model: { ...clearedDraft(model), sendError: "" },
        commands: [
          EnqueueMessage({
            organization: model.organization,
            repository: model.repository,
            workstream: model.workstream,
            key: model.draftKey,
            text: model.draftText,
            images: model.draftImages,
            inboxItems: model.draftInboxItems,
            voice: model.draftVoice?.state === "ready",
          }),
        ],
      };
    },

    StopClicked: () => ({
      model,
      outMessage: OutMessage.StopClicked(),
    }),

    MessageQueued: ({ item }) => ({
      model,
      outMessage: OutMessage.MessageEnqueued({ item }),
    }),

    MessageRefused: ({ error }) => ({
      model: { ...model, sendError: error },
    }),

    VoiceButtonClicked: () => ({
      model,
      commands: [ToggleVoice({ key: model.draftKey, fieldId: inputId(model.instance) })],
    }),

    VoiceResendClicked: () => ({
      model,
      commands: [ResendVoice({ key: model.draftKey, fieldId: inputId(model.instance) })],
    }),

    VoiceRemoveClicked: () => ({
      model,
      commands: [RemoveVoice({ key: model.draftKey })],
    }),

    VoiceChanged: ({ voice }) => ({
      model: { ...model, voice },
    }),

    VoiceTicked: ({ now }) => ({
      model: { ...model, now },
    }),

    CompletedToggleVoice: () => ({ model }),
    CompletedStopVoice: () => ({ model }),
    CompletedResendVoice: () => ({ model }),
    CompletedRemoveVoice: () => ({ model }),
    CompletedSaveDraft: () => ({ model }),
    CompletedScrollLive: () => ({ model }),
    CompletedShowFile: () => ({ model }),
  });
}

export function addDraftInboxItem(
  model: Model,
  item: DraftInboxItem,
): Update.Return<Model, Message> {
  const nextItems = model.draftInboxItems.some((other) => other.id === item.id)
    ? model.draftInboxItems
    : [...model.draftInboxItems, item];
  return {
    model: { ...model, draftInboxItems: nextItems },
    commands: [SaveDraftInboxItems({ key: model.draftKey, inboxItems: nextItems })],
  };
}

export function setSendError(model: Model, error: string): Update.Return<Model, Message> {
  return { model: { ...model, sendError: error }, commands: [] };
}
