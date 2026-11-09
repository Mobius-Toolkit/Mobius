import { loginRequested } from "../ports";

export const ports = {
  inbound: {},
  outbound: { loginRequested },
} as const;

export type DevicesPorts = typeof ports;
