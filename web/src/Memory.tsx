import { use, useCallback, useEffect, useState } from "react";
import {
  listMemoryVersions,
  revertMemory,
  saveMemory,
  type GitHubApp,
  type MemoryVersion,
} from "@/api/api.gen";
import { inset, LinkRow, List, PageHeader, Section } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { diffLines } from "@/lib/diff";
import { LoginContext } from "@/lib/login";
import { dayClock } from "@/lib/time";
import { cn } from "@/lib/utils";
import { TopBar } from "./TopBar";

export function MemoryRepositories({
  apps,
  organizations,
}: {
  apps: GitHubApp[];
  organizations: string[];
}) {
  const repositories = apps.flatMap((app) => app.repositories);

  return (
    <>
      <TopBar title="Memory" back="/settings" />
      <PageHeader title="Memory" />
      {organizations.map((organization) => {
        const organizationRepositories = repositories.filter((repository) =>
          repository.startsWith(`${organization}/`),
        );
        return (
          <Section key={organization} title={organization}>
            {organizationRepositories.length === 0 && (
              <p className={cn("text-muted-foreground", inset)}>
                The Mobius App has no repository in this organization.
              </p>
            )}
            <List>
              {organizationRepositories.map((repository) => {
                const [owner, name] = repository.split("/");
                return (
                  <LinkRow
                    key={repository}
                    link={{ to: "/settings/memory/$owner/$name", params: { owner, name } }}
                    title={repository}
                  />
                );
              })}
            </List>
          </Section>
        );
      })}
    </>
  );
}

function Diff({ before, after }: { before: string; after: string }) {
  const lines = diffLines(before, after);
  if (lines.length === 0) {
    return <p className="text-sm text-muted-foreground">Only the final newline changed.</p>;
  }
  return (
    <pre className="overflow-x-auto rounded-lg border text-sm">
      {lines.map((line, i) => (
        <div
          key={i}
          className={
            line.kind === "added"
              ? "bg-green-500/15 px-2 text-green-800 dark:text-green-300"
              : "bg-red-500/15 px-2 text-red-800 dark:text-red-300"
          }
        >
          {line.kind === "added" ? "+ " : "- "}
          {line.text}
        </div>
      ))}
    </pre>
  );
}

export function Memory({ owner, name }: { owner: string; name: string }) {
  const showLogin = use(LoginContext);
  const [versions, setVersions] = useState<MemoryVersion[]>();
  const [draft, setDraft] = useState<string>();
  const [base, setBase] = useState(0);
  const [saving, setSaving] = useState(false);
  const [reverting, setReverting] = useState<number>();
  const [error, setError] = useState<string>();

  const load = useCallback(() => {
    listMemoryVersions(owner, name)
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 200) {
          setVersions(res.data.data);
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)));
  }, [owner, name, showLogin]);

  useEffect(load, [load]);

  const save = (text: string) => {
    setSaving(true);
    saveMemory(owner, name, { text, base_version: base })
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 204) {
          setError(undefined);
          setDraft(undefined);
          load();
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)))
      .finally(() => setSaving(false));
  };

  const revert = (id: number) => {
    setReverting(id);
    revertMemory(owner, name, id)
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 204) {
          setError(undefined);
          load();
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)))
      .finally(() => setReverting(undefined));
  };

  const current = versions?.[0]?.text ?? "";

  return (
    <>
      <TopBar title={`Memory of ${owner}/${name}`} back="/settings/memory" />
      <PageHeader title={`Memory of ${owner}/${name}`} />
      <div className={cn("grid gap-6", inset)}>
        {error && <Badge variant="destructive">{error}</Badge>}
        {versions && (
          <section className="grid gap-2">
            <div className="flex items-center justify-between gap-4">
              <h3 className="font-medium">Current text</h3>
              {draft === undefined && (
                <Button
                  variant="outline"
                  onClick={() => {
                    setBase(versions[0]?.id ?? 0);
                    setDraft(current);
                  }}
                >
                  Edit
                </Button>
              )}
            </div>
            {draft === undefined ? (
              <pre className="rounded-lg border p-3 text-sm whitespace-pre-wrap">
                {current || "The memory file is empty."}
              </pre>
            ) : (
              <>
                <Textarea
                  aria-label="Memory text"
                  className="font-mono"
                  value={draft}
                  onChange={(event) => setDraft(event.target.value)}
                />
                <div className="flex justify-end gap-2">
                  <Button
                    variant="outline"
                    onClick={() => {
                      setDraft(undefined);
                      setError(undefined);
                    }}
                  >
                    Cancel
                  </Button>
                  <Button pending={saving} onClick={() => save(draft)}>
                    Save
                  </Button>
                </div>
              </>
            )}
          </section>
        )}
        {versions && (
          <section className="grid gap-2">
            <h3 className="font-medium">History</h3>
            {versions.length === 0 && (
              <p className="text-muted-foreground">The memory file has no version.</p>
            )}
            <ul className="grid gap-4">
              {versions.map((version, i) => (
                <li key={version.id} className="grid gap-2">
                  <div className="flex items-center justify-between gap-4">
                    <span className="flex items-center gap-2">
                      <Badge variant="secondary">{version.author}</Badge>
                      {dayClock(version.time)}
                    </span>
                    <Button
                      variant="outline"
                      pending={reverting === version.id}
                      disabled={!version.revertible || reverting !== undefined}
                      onClick={() => revert(version.id)}
                    >
                      Revert
                    </Button>
                  </div>
                  {version.reason && <p className="text-sm break-words">{version.reason}</p>}
                  {!version.revertible && (
                    <p className="text-sm text-muted-foreground">{version.revert_problem}</p>
                  )}
                  <Diff before={versions[i + 1]?.text ?? ""} after={version.text} />
                </li>
              ))}
            </ul>
          </section>
        )}
      </div>
    </>
  );
}
