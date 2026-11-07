import { Schema } from "effect";
import * as Port from "foldkit/port";
import { InboxItem, Workstream } from "./schemas";

export const ports = {
  inbound: {
    unread: Port.inbound(Schema.NullOr(Schema.Number)),
    brief: Port.inbound(Schema.NullOr(Workstream)),
    trayItems: Port.inbound(Schema.Array(InboxItem)),
    workstreams: Port.inbound(Schema.NullOr(Schema.Array(Workstream))),
  },
  outbound: {
    loginRequested: Port.outbound(Schema.Void),
    navigationRequested: Port.outbound(Schema.String),
  },
} as const;

export type ChatPorts = typeof ports;
