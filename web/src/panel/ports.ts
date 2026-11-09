import { Schema } from "effect";
import * as Port from "foldkit/port";
import { Tab } from "./schemas";

export const ports = {
  inbound: { tab: Port.inbound(Tab) },
  outbound: {
    loginRequested: Port.outbound(Schema.Void),
    navigationRequested: Port.outbound(Schema.String),
    tabChanged: Port.outbound(Tab),
    taskAttached: Port.outbound(Schema.Void),
  },
} as const;

export type PanelPorts = typeof ports;
