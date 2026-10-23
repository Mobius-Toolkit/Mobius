import { Fragment, use, useCallback, useEffect, useMemo, useState } from "react";
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/textarea";
import { briefMarkdown } from "@/lib/briefDiff";
import { diffHunks } from "@/lib/diff";
import { useKeyboardHeight } from "@/lib/keyboard";
import { LoginContext } from "@/lib/login";
import { dayClock } from "@/lib/time";
import { cn } from "@/lib/utils";
import { diffMarks } from "./diffMarks";
import { Markdown } from "./Markdown";
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
  const hunks = useMemo(() => diffHunks(before, after), [before, after]);
  if (hunks.length === 0) {
    return <p className="text-sm text-muted-foreground">Only the final newline changed.</p>;
  }
  return (
    <div className="grid gap-2">
      {hunks.map((hunk, i) => (
        <Fragment key={i}>
          {i > 0 && <div role="separator" className="border-t border-dashed" />}
          <div className="rounded-lg border p-3 text-sm">
            <Markdown text={briefMarkdown(hunk)} overrides={diffMarks} />
          </div>
        </Fragment>
      ))}
    </div>
  );
}

function EditDialog({
  repository,
  initial,
  draft,
  onChange,
  saving,
  error,
  onSave,
  onClose,
}: {
  repository: string;
  initial: string;
  draft: string;
  onChange: (text: string) => void;
  saving: boolean;
  error?: string;
  onSave: () => void;
  onClose: () => void;
}) {
  const [discarding, setDiscarding] = useState(false);
  useKeyboardHeight();

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (open || discarding) {
          return;
        }
        if (draft === initial) {
          onClose();
        } else {
          setDiscarding(true);
        }
      }}
    >
      <DialogContent
        aria-describedby={undefined}
        className="grid max-h-[calc(100dvh-var(--keyboard,0px)-2rem)] grid-rows-[auto_minmax(0,1fr)_auto] gap-0 p-0 max-sm:top-auto max-sm:bottom-(--keyboard,0px) max-sm:left-0 max-sm:max-w-none max-sm:translate-x-0 max-sm:translate-y-0 max-sm:rounded-b-none sm:max-h-[85dvh] sm:max-w-2xl"
      >
        <DialogHeader className="p-4 pr-10">
          <DialogTitle>{`Edit the memory of ${repository}`}</DialogTitle>
        </DialogHeader>
        <div className="grid min-h-0 grid-rows-[minmax(0,1fr)_auto] gap-2 px-4 pb-4">
          <Textarea
            aria-label="Memory text"
            className="h-full max-h-full min-h-40 resize-none font-mono"
            value={draft}
            onChange={(event) => onChange(event.target.value)}
          />
          {error && (
            <Badge variant="destructive" className="h-auto w-full justify-start whitespace-normal">
              {error}
            </Badge>
          )}
        </div>
        <DialogFooter className="m-0 max-sm:rounded-none">
          <Button
            variant="outline"
            onClick={() => (draft === initial ? onClose() : setDiscarding(true))}
          >
            Cancel
          </Button>
          <Button pending={saving} onClick={onSave}>
            Save
          </Button>
        </DialogFooter>
        <Dialog open={discarding} onOpenChange={setDiscarding}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Discard your changes?</DialogTitle>
              <DialogDescription>The memory file keeps its text.</DialogDescription>
            </DialogHeader>
            <DialogFooter className="m-0 rounded-none border-0 bg-transparent p-0">
              <Button variant="outline" onClick={() => setDiscarding(false)}>
                Keep editing
              </Button>
              <Button onClick={onClose}>Discard</Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </DialogContent>
    </Dialog>
  );
}

export function Memory({ owner, name }: { owner: string; name: string }) {
  const showLogin = use(LoginContext);
  const [versions, setVersions] = useState<MemoryVersion[]>();
  const [older, setOlder] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [draft, setDraft] = useState<string>();
  const [base, setBase] = useState(0);
  const [saving, setSaving] = useState(false);
  const [reverting, setReverting] = useState<number>();
  const [error, setError] = useState<string>();
  const [saveError, setSaveError] = useState<string>();

  const load = useCallback(() => {
    listMemoryVersions(owner, name)
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 200) {
          setVersions(res.data.data.versions);
          setOlder(res.data.data.older);
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)));
  }, [owner, name, showLogin]);

  useEffect(load, [load]);

  const loadOlder = (before: number) => {
    setLoadingOlder(true);
    listMemoryVersions(owner, name, { before })
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 200) {
          setVersions((current) => [...(current ?? []), ...res.data.data.versions]);
          setOlder(res.data.data.older);
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)))
      .finally(() => setLoadingOlder(false));
  };

  const closeEdit = () => {
    setDraft(undefined);
    setSaveError(undefined);
  };

  const save = (text: string) => {
    setSaving(true);
    saveMemory(owner, name, { text, base_version: base })
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 204) {
          closeEdit();
          load();
        } else {
          setSaveError(res.data.error);
        }
      })
      .catch((err: unknown) => setSaveError(String(err)))
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
      <PageHeader title={`Memory of ${owner}/${name}`} back="/settings/memory" />
      <div className={cn("grid gap-6", inset)}>
        {error && <Badge variant="destructive">{error}</Badge>}
        {versions && (
          <section className="grid gap-2">
            <div className="flex items-center justify-between gap-4">
              <h3 className="font-medium">Current text</h3>
              <Button
                variant="outline"
                onClick={() => {
                  setBase(versions[0]?.id ?? 0);
                  setDraft(current);
                }}
              >
                Edit
              </Button>
            </div>
            <div className="rounded-lg border p-3 text-sm">
              {current ? <Markdown text={current} /> : "The memory file is empty."}
            </div>
          </section>
        )}
        {versions && (
          <section className="grid gap-2">
            <h3 className="font-medium">History</h3>
            {versions.length === 0 && (
              <p className="text-muted-foreground">The memory file has no version.</p>
            )}
            <ul className="grid gap-4">
              {versions.map((version) => (
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
                  <Diff before={version.before} after={version.text} />
                </li>
              ))}
            </ul>
            {older && (
              <Button
                variant="outline"
                pending={loadingOlder}
                onClick={() => loadOlder(versions[versions.length - 1].id)}
              >
                Show older versions
              </Button>
            )}
          </section>
        )}
      </div>
      {draft !== undefined && (
        <EditDialog
          repository={`${owner}/${name}`}
          initial={current}
          draft={draft}
          onChange={setDraft}
          saving={saving}
          error={saveError}
          onSave={() => save(draft)}
          onClose={closeEdit}
        />
      )}
    </>
  );
}
