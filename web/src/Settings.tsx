import { use, useEffect, useState } from "react";
import { getVersion } from "@/api/api.gen";
import { settingsPages } from "@/lib/settings";
import { uiRelease } from "@/lib/build";
import { LoginContext } from "@/lib/login";
import { cn } from "@/lib/utils";
import { inset, LinkRow, List, PageHeader } from "@/components/page";
import { TopBar } from "./TopBar";

export function Settings() {
  const showLogin = use(LoginContext);
  const [server, setServer] = useState("");

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
