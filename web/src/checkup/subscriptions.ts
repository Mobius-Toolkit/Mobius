import * as Subscription from "foldkit/subscription";
import { Message } from "./message";
import type { Model } from "./model";

export const subscriptions = Subscription.make<Model, Message>()(() => ({}));
