import { Option, Schema, Stream } from "effect";
import * as Subscription from "foldkit/subscription";
import { subscriptions as settingSubscriptions } from "@/setting/subscriptions";
import { escapeKey } from "@/ui/escape";
import { Message, toGotSettingMessage } from "./message";
import type { Model } from "./model";

const setting = Subscription.lift(settingSubscriptions)<Model, Message>({
  read: (model) => Option.map(model.sheet, (sheet) => sheet.setting),
  toParentMessage: toGotSettingMessage,
});

const closure = Subscription.make<Model, Message>()((entry) => ({
  closureEscape: entry(
    { open: Schema.Boolean },
    {
      modelToDependencies: (model) => ({
        open: Option.isSome(model.closure) || Option.isSome(model.mcpSheet),
      }),
      dependenciesToStream: ({ open }) =>
        open ? escapeKey(Message.PressedEscape()) : Stream.empty,
    },
  ),
}));

export const subscriptions = Subscription.aggregate(closure, {
  detailsSheetEscape: setting.escape,
});
