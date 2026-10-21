import type { PendingActionRole } from "@/api/api.gen";

export const roleName: Record<PendingActionRole, string> = {
  coordinator: "Coordinator",
  lead: "Lead",
  triager: "Triager",
};

export type Decision = "approve" | "reject";
