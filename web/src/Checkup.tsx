import { Link, type LinkProps } from "@tanstack/react-router";
import { ChevronRightIcon } from "lucide-react";
import { use, useCallback, useEffect, useState, type ReactNode } from "react";
import {
  fixLabels,
  getCheckup,
  getCheckupTools,
  type Checkup as CheckupView,
  type LabelCheck,
  type PermissionCheck,
  type ToolCheck,
} from "@/api/api.gen";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Item, ItemActions, ItemContent, ItemGroup, ItemTitle } from "@/components/ui/item";
import { LoginContext } from "@/lib/login";
import { cn } from "@/lib/utils";
import { TopBar } from "./TopBar";

const fixButton = { create: "Create labels", fix: "Fix labels" };

function LabelStatus({ label }: { label: LabelCheck }) {
  switch (label.status) {
    case "present":
      return <Badge variant="secondary">present</Badge>;
    case "wrong-color":
      return <Badge variant="destructive">wrong color: #{label.found}</Badge>;
    case "wrong-case":
      return <Badge variant="destructive">wrong case: {label.found}</Badge>;
    case "missing":
      return <Badge variant="destructive">missing</Badge>;
  }
}

function PermissionStatus({ permission }: { permission: PermissionCheck }) {
  switch (permission.status) {
    case "present":
      return <Badge variant="secondary">present</Badge>;
    case "not-accepted":
      return (
        <Badge variant="destructive" asChild>
          <a href={permission.url} target="_blank" rel="noreferrer">
            not accepted: accept on GitHub
          </a>
        </Badge>
      );
    case "missing":
      return (
        <Badge variant="destructive" asChild>
          <a href={permission.url} target="_blank" rel="noreferrer">
            missing: add on GitHub
          </a>
        </Badge>
      );
  }
}

function ToolVersion({ tool }: { tool: ToolCheck }) {
  if (tool.status === "") {
    return <Badge variant="secondary">{tool.version}</Badge>;
  }
  return <Badge variant="destructive">{tool.status.replace("-", " ")}</Badge>;
}

function useCheckup(organization: string) {
  const showLogin = use(LoginContext);
  const [checkup, setCheckup] = useState<CheckupView>();
  const [error, setError] = useState<string>();

  const load = useCallback(() => {
    if (!organization) {
      return;
    }
    getCheckup({ organization })
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 200) {
          setCheckup(res.data.data);
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)));
  }, [organization, showLogin]);

  useEffect(load, [load]);

  return { checkup, error, setError, load };
}

function useTools() {
  const showLogin = use(LoginContext);
  const [tools, setTools] = useState<ToolCheck[]>();
  const [error, setError] = useState<string>();

  useEffect(() => {
    getCheckupTools()
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 200) {
          setTools(res.data.data);
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)));
  }, [showLogin]);

  return { tools, error };
}

function CheckupCard({
  title,
  action,
  children,
}: {
  title: string;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <>
      <TopBar title={title} back="/settings/checkup" />
      <Card>
        <CardHeader className={cn(!action && "max-md:hidden")}>
          <CardTitle className="max-md:hidden">{title}</CardTitle>
          {action && <CardAction>{action}</CardAction>}
        </CardHeader>
        <CardContent className="grid gap-6">{children}</CardContent>
      </Card>
    </>
  );
}

function CheckupLink({
  link,
  title,
  needsYou,
}: {
  link: LinkProps;
  title: string;
  needsYou: boolean;
}) {
  return (
    <Item asChild>
      <Link {...link}>
        <ItemContent>
          <ItemTitle>{title}</ItemTitle>
        </ItemContent>
        <ItemActions>
          {needsYou && (
            <Badge className="bg-amber-500/15 text-amber-700 dark:text-amber-400">needs you</Badge>
          )}
          <ChevronRightIcon className="size-4" />
        </ItemActions>
      </Link>
    </Item>
  );
}

function CheckupOrganization({ organization }: { organization: string }) {
  const { checkup } = useCheckup(organization);

  return (
    <section className="grid gap-2">
      <h3 className="font-medium">{organization}</h3>
      <ItemGroup className="gap-1">
        <CheckupLink
          link={{
            to: "/settings/checkup/$organization/permissions",
            params: { organization },
          }}
          title="App permissions"
          needsYou={
            !!checkup &&
            (checkup.permissionsError !== "" ||
              checkup.permissions.some((permission) => permission.status !== "present"))
          }
        />
        <CheckupLink
          link={{
            to: "/settings/checkup/$organization/labels",
            params: { organization },
          }}
          title="Labels"
          needsYou={
            !!checkup &&
            (checkup.labelFix !== "none" ||
              checkup.repositories.some((repository) =>
                repository.labels.some((label) => label.status !== "present"),
              ))
          }
        />
      </ItemGroup>
    </section>
  );
}

export function Checkup({ organizations }: { organizations: string[] }) {
  const { tools } = useTools();

  return (
    <>
      <TopBar title="Checkup" back="/settings" />
      <Card>
        <CardHeader className="max-md:hidden">
          <CardTitle>Checkup</CardTitle>
        </CardHeader>
        <CardContent className="grid gap-4">
          <ItemGroup className="gap-1">
            <CheckupLink
              link={{ to: "/settings/checkup/tools" }}
              title="Tools"
              needsYou={!!tools?.some((tool) => tool.status !== "")}
            />
          </ItemGroup>
          {organizations.map((organization) => (
            <CheckupOrganization key={organization} organization={organization} />
          ))}
        </CardContent>
      </Card>
    </>
  );
}

export function CheckupTools() {
  const { tools, error } = useTools();

  return (
    <CheckupCard title="Tools">
      {error && <Badge variant="destructive">{error}</Badge>}
      {tools && (
        <ul className="divide-y">
          {tools.map((tool) => (
            <li key={tool.name} className="flex items-center justify-between gap-4 py-2">
              <span className="grid min-w-0">
                {tool.name}
                {tool.path && (
                  <span className="text-muted-foreground text-sm break-all">{tool.path}</span>
                )}
              </span>
              <ToolVersion tool={tool} />
            </li>
          ))}
        </ul>
      )}
    </CheckupCard>
  );
}

export function CheckupPermissions({ organization }: { organization: string }) {
  const { checkup, error } = useCheckup(organization);

  return (
    <CheckupCard title="App permissions">
      {error && <Badge variant="destructive">{error}</Badge>}
      {checkup?.permissionsError && <Badge variant="destructive">{checkup.permissionsError}</Badge>}
      {checkup && checkup.permissions.length > 0 && (
        <ul className="divide-y">
          {checkup.permissions.map((permission) => (
            <li key={permission.name} className="flex items-center justify-between gap-4 py-2">
              <span>
                {permission.name}: {permission.level}
              </span>
              <PermissionStatus permission={permission} />
            </li>
          ))}
        </ul>
      )}
    </CheckupCard>
  );
}

export function CheckupLabels({ organization }: { organization: string }) {
  const showLogin = use(LoginContext);
  const { checkup, error, setError, load } = useCheckup(organization);
  const [fixing, setFixing] = useState(false);

  const fix = () => {
    setFixing(true);
    fixLabels({ organization })
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 204) {
          setError(undefined);
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)))
      // The fix can change labels before it fails, so the status loads again.
      .finally(() => {
        load();
        setFixing(false);
      });
  };

  return (
    <CheckupCard
      title="Labels"
      action={
        checkup &&
        checkup.labelFix !== "none" && (
          <Button pending={fixing} onClick={fix}>
            {fixButton[checkup.labelFix]}
          </Button>
        )
      }
    >
      {error && <Badge variant="destructive">{error}</Badge>}
      {checkup?.repositories.length === 0 && (
        <p className="text-muted-foreground">
          The Mobius App has no repository in this organization.
        </p>
      )}
      {checkup?.repositories.map((repository) => (
        <section key={repository.repository} className="grid gap-2">
          <h3 className="font-medium">{repository.repository}</h3>
          <ul className="divide-y">
            {repository.labels.map((label) => (
              <li key={label.name} className="flex items-center justify-between gap-4 py-2">
                <span className="flex items-center gap-2">
                  <span className="size-3 rounded-full" style={{ background: `#${label.color}` }} />
                  {label.name}
                </span>
                <LabelStatus label={label} />
              </li>
            ))}
          </ul>
        </section>
      ))}
    </CheckupCard>
  );
}
