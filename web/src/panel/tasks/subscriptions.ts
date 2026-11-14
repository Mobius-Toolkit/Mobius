import { Option } from "effect";
import * as Subscription from "foldkit/subscription";
import { subscriptions as composerSubscriptions } from "@/composer/subscriptions";
import { subscriptions as settingSubscriptions } from "@/setting/subscriptions";
import { toGotComposerMessage, toGotSettingMessage } from "./message";
import type { Message } from "./message";
import type { Model } from "./model";

const setting = Subscription.lift(settingSubscriptions)<Model, Message>({
  read: (model) => model.sheet,
  toParentMessage: toGotSettingMessage,
});

// The composer is in the page only in the detail view of a task.
const composer = Subscription.lift(composerSubscriptions)<Model, Message>({
  read: (model) => Option.map(model.opened, () => model.composer),
  toParentMessage: toGotComposerMessage,
});

// The Lead chat has its own composer, so the keys must differ from the keys of its Subscriptions.
export const subscriptions = Subscription.aggregate({
  tasksSheetEscape: setting.escape,
  taskDraft: composer.draft,
  taskVoice: composer.voice,
  taskVoiceClock: composer.voiceClock,
  taskVoiceInput: composer.voiceInput,
});
