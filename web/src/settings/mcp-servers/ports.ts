import { backRequested, loginRequested, navigationRequested } from "../ports";

export const ports = {
  inbound: {},
  outbound: { loginRequested, navigationRequested, backRequested },
} as const;

export type McpServersPorts = typeof ports;
