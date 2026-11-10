import { Schema } from "effect";
import * as Port from "foldkit/port";
import { backRequested } from "../ports";
import { SwitchChange, SwitchErrors, SwitchStates } from "../schemas";

// switches is null until the shell has read the switches. switchRequested gives the change of a switch to the shell,
// which sends the request and keeps the error of a failed request.
export const ports = {
  inbound: {
    switches: Port.inbound(Schema.NullOr(SwitchStates)),
    errors: Port.inbound(SwitchErrors),
  },
  outbound: { backRequested, switchRequested: Port.outbound(SwitchChange) },
} as const;

export type SwitchesPorts = typeof ports;
