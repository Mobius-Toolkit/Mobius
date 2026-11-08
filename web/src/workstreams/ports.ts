import { Schema } from "effect";
import * as Port from "foldkit/port";
import { NeedsHuman } from "./schemas";

export const ports = {
  inbound: { needsHuman: Port.inbound(Schema.Array(NeedsHuman)) },
  outbound: { navigationRequested: Port.outbound(Schema.String) },
} as const;

export type WorkstreamsPorts = typeof ports;
