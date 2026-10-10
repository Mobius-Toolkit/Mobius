import { Fragment, use, useCallback, useEffect, useState } from "react";
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
import { Dialog, DialogContent, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/textarea";
import { diffHunks, type DiffLine } from "@/lib/diff";
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

function DiffRows({ hunk, wrap }: { hunk: DiffLine[]; wrap: boolean }) {
  return (
    <div className={cn(wrap ? "w-full" : "w-max min-w-full")}>
      {hunk.map((line, i) => (
        <div
          key={i}
          data-kind={line.kind}
          className={cn(
            "min-h-5 px-2",
            wrap ? "break-words whitespace-pre-wrap" : "whitespace-pre",
            line.kind === "added" && "bg-green-500/15 text-green-800 dark:text-green-300",
            line.kind === "removed" && "bg-red-500/15 text-red-800 dark:text-red-300",
          )}
        >
          {line.kind === "changed"
            ? line.parts.map((part, j) =>
                part.kind === "added" ? (
                  <ins key={j} className="bg-green-500/40 no-underline">
                    {part.text}
                  </ins>
                ) : part.kind === "removed" ? (
                  <del key={j} className="bg-red-500/40 no-underline">
                    {part.text}
                  </del>
                ) : (
                  part.text
                ),
              )
            : line.text}
        </div>
      ))}
    </div>
  );
}

function Hunk({ hunk }: { hunk: DiffLine[] }) {
  return (
    <div className="grid gap-1">
      <div className="flex justify-end">
        <Dialog>
          <DialogTrigger asChild>
            <Button variant="outline" size="sm">
              Full screen
            </Button>
          </DialogTrigger>
          <DialogContent
            aria-describedby={undefined}
            className="top-0 left-0 grid h-dvh max-w-none translate-x-0 translate-y-0 grid-rows-[auto_1fr] rounded-none sm:max-w-none"
          >
            <DialogTitle className="pr-8">Diff</DialogTitle>
            <pre className="overflow-y-auto rounded-lg border text-sm">
              <DiffRows hunk={hunk} wrap />
            </pre>
          </DialogContent>
        </Dialog>
      </div>
      <pre className="overflow-x-auto rounded-lg border text-sm">
        <DiffRows hunk={hunk} wrap={false} />
      </pre>
    </div>
  );
}

function Diff({ before, after }: { before: string; after: string }) {
  const hunks = diffHunks(before, after);
  if (hunks.length === 0) {
    return <p className="text-sm text-muted-foreground">Only the final newline changed.</p>;
  }
  return (
    <div className="grid gap-2">
      {hunks.map((hunk, i) => (
        <Fragment key={i}>
          {i > 0 && <div role="separator" className="border-t border-dashed" />}
          <Hunk hunk={hunk} />
        </Fragment>
      ))}
    </div>
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
