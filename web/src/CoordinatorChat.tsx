import { PageHeader } from "@/components/page";
import type { Approval, ActionItem } from "@/lib/actions";
import { Conversation } from "./Conversation";
import { TopBar } from "./TopBar";

export function CoordinatorChat({
  organizations,
  organization,
  inbox,
  unread,
  source,
}: {
  organizations: string[];
  organization: string;
  inbox: ActionItem[];
  unread?: number;
  source?: EventSource;
}) {
  if (!organizations.includes(organization)) {
    return (
      <>
        <TopBar title="Chat" />
        <div className="grid grow content-start gap-6 p-4 md:mx-auto md:my-6 md:w-[min(48rem,calc(100%-3rem))] md:grow-0 md:self-start md:rounded-xl md:border md:bg-card md:p-6">
          <PageHeader title="Chat" />
          <p className="text-muted-foreground">
            Mobius reads the repositories from GitHub. The Coordinator chat opens after this step.
          </p>
        </div>
      </>
    );
  }
  return (
    <>
      <TopBar title="Chat" />
      <Conversation
        key={organization}
        organization={organization}
        repository=""
        workstream={0}
        agent="Coordinator"
        source={source}
        unread={unread}
        approvals={inbox.filter(
          (item): item is Approval =>
            item.organization === organization && item.action?.role === "coordinator",
        )}
        head={
          <>
            <h2 className="text-base font-semibold">Chat</h2>
          </>
        }
      />
    </>
  );
}
