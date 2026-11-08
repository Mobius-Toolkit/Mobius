import { Schema } from "effect";
import * as Port from "foldkit/port";

export const ports = {
  inbound: {},
  outbound: {
    loginRequested: Port.outbound(Schema.Void),
    navigationRequested: Port.outbound(Schema.String),
  },
} as const;

export type TasksPorts = typeof ports;
