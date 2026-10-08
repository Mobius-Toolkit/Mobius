import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
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
        <Card className="m-4 grow self-start md:m-6">
          <CardHeader className="max-md:hidden">
            <CardTitle>Chat</CardTitle>
          </CardHeader>
          <CardContent className="text-muted-foreground">
            Mobius reads the repositories from GitHub. The Triager chat opens after this step.
          </CardContent>
        </Card>
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
