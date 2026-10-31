import { useRouter, type LinkProps } from "@tanstack/react-router";
import { use, useCallback, useEffect, useState, type ReactNode } from "react";
import {
  fixLabels,
  getCheckup,
  getCheckupTools,
  upgradeCheckupTool,
  type Checkup as CheckupView,
  type LabelCheck,
  type PermissionCheck,
  type ToolCheck,
} from "@/api/api.gen";
import {
  ErrorBadge,
  inset,
  LinkRow,
  List,
  PageHeader,
  Row,
  rowClass,
  Section,
} from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { LoginContext } from "@/lib/login";
import { showNotice } from "@/lib/notice";
import { addCoordinatorMessage } from "@/lib/outbox";
import { useShell } from "@/lib/shell";
import { cn } from "@/lib/utils";
import { chatParams } from "@/lib/workstreams";
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
  return (
    <Badge variant="destructive">
      {tool.outdated ? "outdated" : tool.status.replace("-", " ")}
    </Badge>
  );
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

  return { tools, error, setTools };
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
      <PageHeader title={title} back="/settings/checkup">
        {action}
      </PageHeader>
      {children}
    </>
  );
}

function CheckupLink({
  link,
  title,
  needsYou,
  busy,
}: {
  link: LinkProps;
  title: string;
  needsYou: boolean;
  busy?: boolean;
}) {
  return (
    <LinkRow link={link} title={title} busy={busy}>
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
        <CheckupLink
          link={{
            to: "/settings/checkup/$organization/workstreams",
            params: { organization },
          }}
          title="Workstreams"
          needsYou={
            !!checkup &&
            (checkup.maintenanceWorkstream === 0 ||
              (checkup.skillRepository !== "" && checkup.skillsWorkstream === 0))
          }
        />
      </List>
    </Section>
  );
}

export function Checkup({ organizations }: { organizations: string[] }) {
  const { tools, error } = useTools();

  return (
    <>
      <TopBar title="Checkup" back="/settings" />
      <PageHeader title="Checkup" />
      <Section title="Server">
        <List>
          <CheckupLink
            link={{ to: "/settings/checkup/tools" }}
            title="Tools"
            busy={!tools && !error}
            needsYou={
              !!tools?.some((tool) => tool.outdated || (tool.status !== "" && tool.required))
            }
          />
        </List>
      </Section>
      {organizations.map((organization) => (
        <CheckupOrganization key={organization} organization={organization} />
      ))}
    </>
  );
}

function ToolUpgrade({
  tool,
  onClose,
  onUpgraded,
}: {
  tool: ToolCheck;
  onClose: () => void;
  onUpgraded: (tools: ToolCheck[]) => void;
}) {
  const showLogin = use(LoginContext);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");

  const upgrade = () => {
    setPending(true);
    setError("");
    upgradeCheckupTool({ package: tool.package })
      .then((res) => {
        if (res.status === 200) {
          onUpgraded(res.data.data);
          showNotice(`Upgraded ${tool.name} to ${tool.newest}`);
        } else if (res.status === 401) {
          showLogin();
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)))
      .finally(() => setPending(false));
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        aria-describedby={undefined}
        className="max-sm:top-auto max-sm:bottom-0 max-sm:left-0 max-sm:max-w-none max-sm:translate-x-0 max-sm:translate-y-0 max-sm:rounded-b-none sm:max-w-lg"
      >
        <DialogHeader>
          <DialogTitle className="wrap-anywhere">{tool.name}</DialogTitle>
        </DialogHeader>
        <div className="grid gap-2">
          <p>
            Installed: <span className="wrap-anywhere">{tool.version}</span>
          </p>
          <p>
            Newest: <span className="wrap-anywhere">{tool.newest}</span>
          </p>
          {error && <p className="text-destructive wrap-anywhere">{error}</p>}
          <Button pending={pending} disabled={!tool.outdated} onClick={upgrade}>
            Upgrade
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function ToolText({ tool, grow }: { tool: ToolCheck; grow?: boolean }) {
  return (
    <span className={cn("grid min-w-0", grow && "grow text-left")}>
      {tool.name}
      {tool.status === "" && (
        <span className="text-muted-foreground text-sm break-words">
          {tool.outdated ? `${tool.version} · newest ${tool.newest}` : tool.version}
        </span>
      )}
      {tool.status !== "" && !tool.required && (
        <span className="text-muted-foreground text-sm break-words">
          {tool.status.replace("-", " ")}
        </span>
      )}
      {tool.path && <span className="text-muted-foreground text-sm break-all">{tool.path}</span>}
    </span>
  );
}

export function CheckupTools() {
  const { tools, error, setTools } = useTools();
  const [openName, setOpenName] = useState<string>();
  const open = tools?.find((tool) => tool.name === openName);

  return (
    <CheckupCard title="Tools">
      {error && <ErrorBadge>{error}</ErrorBadge>}
      {tools && (
        <List>
          {tools.map((tool) => {
            const problem =
              tool.outdated || (tool.status !== "" && tool.required) ? (
                <ToolProblem tool={tool} />
              ) : null;
            if (!tool.package) {
              return (
                <Row key={tool.name} className="justify-between">
                  <ToolText tool={tool} />
                  {problem}
                </Row>
              );
            }
            return (
              <li key={tool.name}>
                <button
                  type="button"
                  onClick={() => setOpenName(tool.name)}
                  className={cn(rowClass, "w-full justify-between hover:bg-muted")}
                >
                  <ToolText tool={tool} grow />
                  {problem}
                </button>
              </li>
            );
          })}
        </List>
      )}
      {open && (
        <ToolUpgrade tool={open} onClose={() => setOpenName(undefined)} onUpgraded={setTools} />
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

const maintenanceWorkstreamRequest = (
  repository: string,
) => `Create the maintenance Workstream for ${repository}, with the labels \`mobius:maintenance\` and \`mobius:evergreen\`.

Use this Brief:

**Goal**
Keep the default branch of each repository of the organization healthy, so that the work of the other Workstreams can merge. This Workstream has no end.

**Scope**
- Each task issue that Mobius creates for a repository of the organization.
- Failures of the CI on the default branch of each repository, and flaky tests.
- The development tools, the scripts, the project setup, and the CI configuration.
- Small defects of the applications that no open Workstream covers.
- When the cause of a failure is outside the repository, close the issue with the cause in a comment.

**Limits**
- Do not add a feature or change a user flow.
- Do not take work that belongs to another open Workstream.`;

const skillsWorkstreamRequest = (
  issueRepository: string,
  skillRepository: string,
) => `Create the Skills Workstream in ${issueRepository}, with the labels \`mobius:skills\` and \`mobius:evergreen\`. It gets the proposals of \`propose_skill\` for ${skillRepository}.

Use this Brief:

**Goal**
Keep the skills of the agents in ${skillRepository} correct and current. This Workstream has no end.

**Scope**
- Each skill proposal that an agent sends with \`propose_skill\`.
- The task issues and the pull requests that add or change a skill are in ${skillRepository}.

**Limits**
- Change only ${skillRepository}.
- Do not add a skill that the Owner did not approve.`;

export function CheckupWorkstreams({ organization }: { organization: string }) {
  const { selectOrganization } = useShell();
  const router = useRouter();
  const { checkup, error } = useCheckup(organization);
  const [creating, setCreating] = useState<string>();
  const [createError, setCreateError] = useState<string>();

  const create = (key: string, request: string) => {
    setCreating(key);
    const pathname = router.state.location.pathname;
    addCoordinatorMessage(organization, request)
      .then(() => {
        selectOrganization(organization);
        if (router.state.location.pathname === pathname) {
          return router.navigate({ to: "/chat" });
        }
      })
      .catch((err: unknown) => {
        setCreateError(String(err));
        setCreating(undefined);
      });
  };

  return (
    <CheckupCard title="Workstreams">
      {(error ?? createError) && <ErrorBadge>{error ?? createError}</ErrorBadge>}
      <p className={cn("text-muted-foreground", inset)}>
        The maintenance Workstream gets each task issue that Mobius creates and the CI failures of
        the default branch of each repository of the organization. The Skills Workstream gets the
        skill proposals of the agents. Create opens the chat with a draft for your approval.
      </p>
      {checkup?.issueRepositoryError && <ErrorBadge>{checkup.issueRepositoryError}</ErrorBadge>}
      {checkup && !checkup.issueRepositoryError && (
        <Section title={checkup.issueRepository}>
          <List>
            {checkup.maintenanceWorkstream > 0 ? (
              <LinkRow
                link={{
                  to: "/workstreams/$owner/$name/$number",
                  params: chatParams({
                    repository: checkup.issueRepository,
                    number: checkup.maintenanceWorkstream,
                  }),
                }}
                title={<span className="whitespace-nowrap">Maintenance Workstream</span>}
              >
                <span className="text-muted-foreground">#{checkup.maintenanceWorkstream}</span>
              </LinkRow>
            ) : (
              <Row className="justify-between">
                <span className="whitespace-nowrap">Maintenance Workstream</span>
                <Button
                  size="sm"
                  variant="outline"
                  pending={creating === "maintenance"}
                  onClick={() =>
                    create("maintenance", maintenanceWorkstreamRequest(checkup.issueRepository))
                  }
                >
                  Create
                </Button>
              </Row>
            )}
            {checkup.skillRepository !== "" &&
              (checkup.skillsWorkstream > 0 ? (
                <LinkRow
                  link={{
                    to: "/workstreams/$owner/$name/$number",
                    params: chatParams({
                      repository: checkup.skillsWorkstreamRepository,
                      number: checkup.skillsWorkstream,
                    }),
                  }}
                  title={<span className="whitespace-nowrap">Skills Workstream</span>}
                >
                  <span className="text-muted-foreground">
                    {checkup.skillsWorkstreamRepository === checkup.issueRepository
                      ? ""
                      : checkup.skillsWorkstreamRepository}
                    #{checkup.skillsWorkstream}
                  </span>
                </LinkRow>
              ) : (
                <Row className="justify-between">
                  <span className="whitespace-nowrap">Skills Workstream</span>
                  <Button
                    size="sm"
                    variant="outline"
                    pending={creating === "skills"}
                    onClick={() =>
                      create(
                        "skills",
                        skillsWorkstreamRequest(checkup.issueRepository, checkup.skillRepository),
                      )
                    }
                  >
                    Create
                  </Button>
                </Row>
              ))}
          </List>
        </Section>
      )}
    </CheckupCard>
  );
}
