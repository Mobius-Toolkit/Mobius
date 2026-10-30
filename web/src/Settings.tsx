import { use, useEffect, useState } from "react";
import { getVersion } from "@/api/api.gen";
import { Badge } from "@/components/ui/badge";
import { settingsPages } from "@/lib/settings";
import { useShell } from "@/lib/shell";
import { switchKinds } from "@/lib/switches";
import { useVoiceLanguage } from "@/lib/voiceLanguage";
import { uiRelease } from "@/lib/build";
import { LoginContext } from "@/lib/login";
import { cn } from "@/lib/utils";
import { inset, LinkRow, List, PageHeader } from "@/components/page";
import { TopBar } from "./TopBar";

export function Settings() {
  const showLogin = use(LoginContext);
  const { switches, switchErrors } = useShell();
  const [server, setServer] = useState("");
  const { voice } = useVoiceLanguage();
  const language = voice?.languages.find((item) => item.code === voice.language)?.name;

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

  const off = switchKinds
    .filter(({ kind }) => switches?.[kind] === false)
    .map(({ title }) => `${title} off`);
  const state = switches && (off.length === 0 ? "All on" : off.join(", "));
  const switchError = Object.values(switchErrors).some(Boolean);

  return (
    <>
      <TopBar title="Settings" />
      <PageHeader title="Settings" />
      <List>
        {settingsPages.map((page) => (
          <LinkRow key={page.path} link={{ to: page.path }} title={page.title}>
            {page.path === "/settings/switches" && (
              <>
                {state && <span className="text-sm text-muted-foreground">{state}</span>}
                {switchError && <Badge variant="destructive">Error</Badge>}
              </>
            )}
            {page.path === "/settings/voice" && (
              <span className="text-sm text-muted-foreground">{language}</span>
            )}
          </LinkRow>
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
