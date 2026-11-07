import { Schema } from "effect";
import * as Port from "foldkit/port";
import type { InboxItem, Workstream } from "@/api/api.gen";

export const ports = {
  inbound: {
    unread: Port.inbound(Schema.NullOr(Schema.Number)),
    brief: Port.inbound(Schema.NullOr(Schema.Unknown) as Schema.Codec<Workstream | null>),
    trayItems: Port.inbound(Schema.Unknown as Schema.Codec<InboxItem[]>),
    workstreams: Port.inbound(Schema.NullOr(Schema.Unknown) as Schema.Codec<Workstream[] | null>),
  },
  outbound: {
    loginRequested: Port.outbound(Schema.Void),
    navigationRequested: Port.outbound(Schema.String),
  },
} as const;

export type ChatPorts = typeof ports;
