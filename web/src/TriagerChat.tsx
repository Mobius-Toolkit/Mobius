import { PageHeader } from "@/components/page";
import { Conversation } from "./Conversation";
import { TopBar } from "./TopBar";

export function TriagerChat({
  organizations,
  organization,
  unread,
  source,
}: {
  organizations: string[];
  organization: string;
  unread?: number;
  source?: EventSource;
}) {
  if (!organizations.includes(organization)) {
    return (
      <>
        <TopBar title="Chat" />
        <div className="grid grow content-start gap-6 p-4 md:p-6">
          <PageHeader title="Chat" />
          <p className="text-muted-foreground">
            Mobius reads the repositories from GitHub. The Triager chat opens after this step.
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
        agent="Triager"
        source={source}
        unread={unread}
        head={
          <>
            <h2 className="font-semibold">Chat</h2>
          </>
        }
      />
    </>
  );
}
