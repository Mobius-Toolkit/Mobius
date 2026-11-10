import { Schema } from "effect";
import * as Port from "foldkit/port";
import { backRequested, loginRequested, navigationRequested } from "../ports";
import { GitHubApp } from "../schemas";

// apps and organizations come from the shell. The page of the repositories needs them.
export const ports = {
  inbound: {
    apps: Port.inbound(Schema.Array(GitHubApp)),
    organizations: Port.inbound(Schema.Array(Schema.String)),
  },
  outbound: { loginRequested, navigationRequested, backRequested },
} as const;

export type MemoryPorts = typeof ports;
