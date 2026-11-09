import * as Subscription from "foldkit/subscription";
import { subscriptions as settingSubscriptions } from "@/setting/subscriptions";
import { toGotSettingMessage } from "./message";
import type { Message } from "./message";
import type { Model } from "./model";

const setting = Subscription.lift(settingSubscriptions)<Model, Message>({
  read: (model) => model.sheet,
  toParentMessage: toGotSettingMessage,
});

export const subscriptions = { tasksSheetEscape: setting.escape };
