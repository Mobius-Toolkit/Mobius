import * as Port from "foldkit/port";
import * as Subscription from "foldkit/subscription";
import { Message } from "./message";
import type { Model } from "./model";
import { ports } from "./ports";

export const subscriptions = Subscription.make<Model, Message>()(() => ({
  organizationsPort: Port.subscriptionEntry(ports.inbound.organizations, (organizations) =>
    Message.ReceivedOrganizations({ organizations }),
  ),
  pausesPort: Port.subscriptionEntry(ports.inbound.pauses, (pauses) =>
    Message.ReceivedPauses({ pauses }),
  ),
  fixPort: Port.subscriptionEntry(ports.inbound.fixRequested, () => Message.ClickedFix()),
}));
