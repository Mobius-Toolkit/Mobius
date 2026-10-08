import { settingsPages } from "@/lib/settings";
import { LinkRow, List, PageHeader } from "@/components/page";
import { TopBar } from "./TopBar";

export function Settings() {
  return (
    <>
      <TopBar title="Settings" />
      <PageHeader title="Settings" />
      <List>
        {settingsPages.map((page) => (
          <LinkRow key={page.path} link={{ to: page.path }} title={page.title} />
        ))}
      </List>
    </>
  );
}
