import { use, useEffect, useState } from "react";
import { getVersion } from "@/api/api.gen";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { settingsPages } from "@/lib/settings";
import { useShell } from "@/lib/shell";
import { switchKinds } from "@/lib/switches";
import { uiRelease } from "@/lib/build";
import { LoginContext } from "@/lib/login";
import { cn } from "@/lib/utils";
import { ErrorBadge, inset, LinkRow, List, PageHeader, Row, Section } from "@/components/page";
import { TopBar } from "./TopBar";

export function Settings() {
  const showLogin = use(LoginContext);
  const { switches, changeSwitch } = useShell();
  const [server, setServer] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    getVersion()
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 200) {
          setServer(res.data.data.release);
        }
      })
      .catch(() => undefined);
  }, [showLogin]);

  return (
    <>
      <TopBar title="Settings" />
      <PageHeader title="Settings" />
      <Section title="Switches">
        <List>
          {switchKinds.map(({ kind, title, description }) => (
            <Row key={kind}>
              <div className="grid min-w-0 grow gap-0.5">
                <Label htmlFor={`switch-${kind}`}>{title}</Label>
                <span className="text-sm text-muted-foreground">{description}</span>
              </div>
              <Switch
                id={`switch-${kind}`}
                checked={switches?.[kind] ?? true}
                onCheckedChange={(on) => void changeSwitch(kind, on).then(setError)}
              />
            </Row>
          ))}
        </List>
        {error && <ErrorBadge>{error}</ErrorBadge>}
      </Section>
      <List>
        {settingsPages.map((page) => (
          <LinkRow key={page.path} link={{ to: page.path }} title={page.title} />
        ))}
      </List>
      <dl className={cn("mt-4 grid gap-1 text-xs text-muted-foreground", inset)}>
        {server && (
          <div className="flex gap-4">
            <dt className="w-16">Server</dt>
            <dd>{server}</dd>
          </div>
        )}
        {uiRelease && (
          <div className="flex gap-4">
            <dt className="w-16">Browser</dt>
            <dd>{uiRelease}</dd>
          </div>
        )}
      </dl>
    </>
  );
}
