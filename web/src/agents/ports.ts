import { Schema } from "effect";
import * as Port from "foldkit/port";

// titleChanged gives the title of the open agent, or an empty text when no agent is open. backRequested is the back
// button of the top bar.
export const ports = {
  inbound: { backRequested: Port.inbound(Schema.Void) },
  outbound: {
    loginRequested: Port.outbound(Schema.Void),
    titleChanged: Port.outbound(Schema.String),
  },
} as const;

export type AgentsPorts = typeof ports;
