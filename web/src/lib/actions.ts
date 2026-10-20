import type { InboxItem, PendingAction, PendingActionRole } from "@/api/api.gen";

export const roleName: Record<PendingActionRole, string> = {
  coordinator: "Coordinator",
  lead: "Lead",
  triager: "Triager",
};

export type Approval = InboxItem & { action: PendingAction };

export type Decision = "approve" | "reject";
