import { Link } from "@tanstack/react-router";
import { ChevronRightIcon } from "lucide-react";
import { use, useCallback, useEffect, useState } from "react";
import {
  listMemoryVersions,
  revertMemory,
  saveMemory,
  type GitHubApp,
  type MemoryVersion,
} from "@/api/api.gen";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Item, ItemActions, ItemContent, ItemGroup, ItemTitle } from "@/components/ui/item";
import { Textarea } from "@/components/ui/textarea";
import { diffLines } from "@/lib/diff";
import { LoginContext } from "@/lib/login";
import { dayClock } from "@/lib/time";
import { BackButton } from "./BackButton";

export function MemoryRepositories({
  apps,
  organization,
}: {
  apps: GitHubApp[];
  organization: string;
}) {
  const repositories = apps
    .flatMap((app) => app.repositories)
    .filter((repository) => repository.startsWith(`${organization}/`));

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <BackButton parent="/settings" />
          Memory
        </CardTitle>
      </CardHeader>
      <CardContent>
        {repositories.length === 0 && (
          <p className="text-muted-foreground">
            The Mobius App has no repository in this organization.
          </p>
        )}
        <ItemGroup className="gap-1">
          {repositories.map((repository) => {
            const [owner, name] = repository.split("/");
            return (
              <Item key={repository} asChild>
                <Link to="/settings/memory/$owner/$name" params={{ owner, name }}>
                  <ItemContent>
                    <ItemTitle>{repository}</ItemTitle>
                  </ItemContent>
                  <ItemActions>
                    <ChevronRightIcon className="size-4" />
                  </ItemActions>
                </Link>
              </Item>
            );
          })}
        </ItemGroup>
      </CardContent>
    </Card>
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
    saveMemory(owner, name, { text })
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
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <BackButton parent="/settings/memory" />
          Memory of {owner}/{name}
        </CardTitle>
      </CardHeader>
      <CardContent className="grid gap-6">
        {error && <Badge variant="destructive">{error}</Badge>}
        {versions && (
          <section className="grid gap-2">
            <div className="flex items-center justify-between gap-4">
              <h3 className="font-medium">Current text</h3>
              {draft === undefined && (
                <Button variant="outline" onClick={() => setDraft(current)}>
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
                  <Button variant="outline" onClick={() => setDraft(undefined)}>
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
                      disabled={reverting !== undefined}
                      onClick={() => revert(version.id)}
                    >
                      Revert
                    </Button>
                  </div>
                  <Diff before={versions[i + 1]?.text ?? ""} after={version.text} />
                </li>
              ))}
            </ul>
          </section>
        )}
      </CardContent>
    </Card>
  );
}
