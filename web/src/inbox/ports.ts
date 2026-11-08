import { organizationRequested, ports as chatPorts } from "@/chat/ports";

// The Commands of the chat program name these Ports, so the Inbox program uses the same Port values.
export const ports = {
  inbound: { trayItems: chatPorts.inbound.trayItems },
  outbound: { ...chatPorts.outbound, organizationRequested },
} as const;

export type InboxPorts = typeof ports;
