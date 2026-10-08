import type { LinkProps } from "@tanstack/react-router";
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
import { ErrorBadge, inset, LinkRow, List, PageHeader, Row, Section } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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

function ToolProblem({ tool }: { tool: ToolCheck }) {
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
      <TopBar title={title} back="/settings/checkup">
        {action && <div className="mr-2 ml-auto">{action}</div>}
      </TopBar>
      <PageHeader title={title}>{action}</PageHeader>
      {children}
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
    <LinkRow link={link} title={title}>
      {needsYou && (
        <Badge className="bg-amber-500/15 text-amber-700 dark:text-amber-400">needs you</Badge>
      )}
    </LinkRow>
  );
}

function CheckupOrganization({ organization }: { organization: string }) {
  const { checkup } = useCheckup(organization);

  return (
    <Section title={organization}>
      <List>
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
      </List>
    </Section>
  );
}

export function Checkup({ organizations }: { organizations: string[] }) {
  const { tools } = useTools();

  return (
    <>
      <TopBar title="Checkup" back="/settings" />
      <PageHeader title="Checkup" />
      <Section title="Server">
        <List>
          <CheckupLink
            link={{ to: "/settings/checkup/tools" }}
            title="Tools"
            needsYou={!!tools?.some((tool) => tool.status !== "")}
          />
        </List>
      </Section>
      {organizations.map((organization) => (
        <CheckupOrganization key={organization} organization={organization} />
      ))}
    </>
  );
}

export function CheckupTools() {
  const { tools, error } = useTools();

  return (
    <CheckupCard title="Tools">
      {error && <ErrorBadge>{error}</ErrorBadge>}
      {tools && (
        <List>
          {tools.map((tool) => (
            <Row key={tool.name} className="justify-between">
              <span className="grid min-w-0">
                {tool.name}
                {tool.status === "" && (
                  <span className="text-muted-foreground text-sm break-words">{tool.version}</span>
                )}
                {tool.path && (
                  <span className="text-muted-foreground text-sm break-all">{tool.path}</span>
                )}
              </span>
              {tool.status !== "" && <ToolProblem tool={tool} />}
            </Row>
          ))}
        </List>
      )}
    </CheckupCard>
  );
}

export function CheckupPermissions({ organization }: { organization: string }) {
  const { checkup, error } = useCheckup(organization);

  return (
    <CheckupCard title="App permissions">
      {error && <ErrorBadge>{error}</ErrorBadge>}
      {checkup?.permissionsError && <ErrorBadge>{checkup.permissionsError}</ErrorBadge>}
      {checkup && checkup.permissions.length > 0 && (
        <List>
          {checkup.permissions.map((permission) => (
            <Row key={permission.name} className="justify-between">
              <span>
                {permission.name}: {permission.level}
              </span>
              <PermissionStatus permission={permission} />
            </Row>
          ))}
        </List>
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
      {error && <ErrorBadge>{error}</ErrorBadge>}
      {checkup?.repositories.length === 0 && (
        <p className={cn("text-muted-foreground", inset)}>
          The Mobius App has no repository in this organization.
        </p>
      )}
      {checkup?.repositories.map((repository) => (
        <Section key={repository.repository} title={repository.repository}>
          <List>
            {repository.labels.map((label) => (
              <Row key={label.name} className="justify-between">
                <span className="flex items-center gap-2">
                  <span className="size-3 rounded-full" style={{ background: `#${label.color}` }} />
                  {label.name}
                </span>
                <LabelStatus label={label} />
              </Row>
            ))}
          </List>
        </Section>
      ))}
    </CheckupCard>
  );
}
