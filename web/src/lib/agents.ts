import type { Agent } from "@/api/api.gen";

// The state badge shows a pause, so its reason does not repeat in the details.
export const queueDetail = ({ wait, queueReason }: Agent) => (wait === "paused" ? "" : queueReason);
