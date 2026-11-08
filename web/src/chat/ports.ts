import { Schema } from "effect";
import * as Port from "foldkit/port";
import { InboxItem, Workstream } from "./schemas";

export const ports = {
  inbound: {
    brief: Port.inbound(Schema.NullOr(Workstream)),
    trayItems: Port.inbound(Schema.Array(InboxItem)),
  },
  outbound: {
    loginRequested: Port.outbound(Schema.Void),
    navigationRequested: Port.outbound(Schema.String),
  },
} as const;

export type ChatPorts = typeof ports;
