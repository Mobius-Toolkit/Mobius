import { Schema } from "effect";
import * as Port from "foldkit/port";
import { loginRequested } from "../ports";
import { GitHubApp } from "../schemas";

export const ports = {
  inbound: { apps: Port.inbound(Schema.Array(GitHubApp)) },
  outbound: { loginRequested },
} as const;

export type GitHubPorts = typeof ports;
