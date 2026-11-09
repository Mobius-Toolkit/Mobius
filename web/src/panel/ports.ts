import { Schema } from "effect";
import * as Port from "foldkit/port";
import { Tab } from "./schemas";

export const ports = {
  inbound: { tab: Port.inbound(Tab) },
  outbound: {
    loginRequested: Port.outbound(Schema.Void),
    tabChanged: Port.outbound(Tab),
  },
} as const;

export type PanelPorts = typeof ports;
