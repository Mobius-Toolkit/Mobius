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
        <div className="grid grow content-start gap-6 p-4 md:mx-auto md:my-6 md:w-[min(48rem,calc(100%-3rem))] md:grow-0 md:self-start md:rounded-xl md:border md:bg-card md:p-6">
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
            <h2 className="text-base font-semibold">Chat</h2>
          </>
        }
      />
    </>
  );
}
