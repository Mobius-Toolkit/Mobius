import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Conversation } from "./Conversation";

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
      <Card className="m-4 grow self-start md:m-6">
        <CardHeader>
          <CardTitle>Chat</CardTitle>
        </CardHeader>
        <CardContent className="text-muted-foreground">
          Mobius reads the repositories from GitHub. The Triager chat opens after this step.
        </CardContent>
      </Card>
    );
  }
  return (
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
  );
}
