import { backRequested, loginRequested } from "../ports";

export const ports = {
  inbound: {},
  outbound: { loginRequested, backRequested },
} as const;

export type RolesPorts = typeof ports;
