import { Schema } from "effect";
import * as Port from "foldkit/port";
import { loginRequested, navigationRequested } from "../ports";
import { SwitchErrors, SwitchStates } from "../schemas";

// switches is null until the shell has read the switches.
export const ports = {
  inbound: {
    switches: Port.inbound(Schema.NullOr(SwitchStates)),
    errors: Port.inbound(SwitchErrors),
  },
  outbound: { loginRequested, navigationRequested },
} as const;

export type OverviewPorts = typeof ports;
