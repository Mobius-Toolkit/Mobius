import { Schema } from "effect";
import * as Port from "foldkit/port";
import { FixButton, Pause } from "./schemas";

// organizations and pauses come from the shell. fixRequested is the fix button of the top bar, and fixButtonChanged
// gives that button its text and state, or null when the page has no fix button. chatRequested gives the organization
// of a new request to the Coordinator.
export const ports = {
  inbound: {
    organizations: Port.inbound(Schema.Array(Schema.String)),
    pauses: Port.inbound(Schema.Array(Pause)),
    fixRequested: Port.inbound(Schema.Void),
  },
  outbound: {
    loginRequested: Port.outbound(Schema.Void),
    navigationRequested: Port.outbound(Schema.String),
    backRequested: Port.outbound(Schema.Void),
    chatRequested: Port.outbound(Schema.String),
    fixButtonChanged: Port.outbound(Schema.NullOr(FixButton)),
  },
} as const;

export type CheckupPorts = typeof ports;
