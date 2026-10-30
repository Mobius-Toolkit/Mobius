import { ChevronDownIcon, ChevronRightIcon } from "lucide-react";
import { Fragment, use, useEffect, useState } from "react";
import {
  listHarnesses,
  listRoles,
  resetRole,
  setRole,
  type HarnessChoices,
  type Role,
  type resetRoleResponse,
  type setRoleResponse,
} from "@/api/api.gen";
import { ErrorBadge, List, PageHeader, rowClass } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet";
import { Spinner } from "@/components/ui/spinner";
import { LoginContext } from "@/lib/login";
import { cn } from "@/lib/utils";
import { TopBar } from "./TopBar";

const roleName = (role: Role) => role.role.charAt(0).toUpperCase() + role.role.slice(1);

const keepEffort = (effort: string, efforts: string[]) =>
  efforts.includes(effort) ? effort : (efforts[0] ?? "");

function UiBadge() {
  return (
    <Badge variant="secondary" className="shrink-0">
      UI
    </Badge>
  );
}

function RoleRow({ role, onOpen }: { role: Role; onOpen: () => void }) {
  const values = [
    { text: role.harness, ui: role.ui.harness },
    { text: role.model, ui: role.ui.model },
    ...(role.effort ? [{ text: role.effort, ui: role.ui.effort }] : []),
    { text: `max ${role.max}`, ui: role.ui.max },
  ];
  return (
    <li>
      <button
        type="button"
        className={cn(rowClass, "w-full text-left hover:bg-muted")}
        onClick={onOpen}
      >
        <span className="grid min-w-0 grow gap-0.5">
          <span>{roleName(role)}</span>
          <span className="flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-sm text-muted-foreground">
            {values.map((value, index) => (
              <Fragment key={value.text}>
                {index > 0 && <span aria-hidden>·</span>}
                <span className="break-all">{value.text}</span>
                {value.ui && <UiBadge />}
              </Fragment>
            ))}
          </span>
        </span>
        <ChevronRightIcon className="size-4 shrink-0" />
      </button>
    </li>
  );
}

function Choice({
  id,
  value,
  values,
  loading,
  onSelect,
}: {
  id: string;
  value: string;
  values: string[];
  loading: boolean;
  onSelect: (value: string) => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button id={id} variant="outline" className="min-w-0 grow justify-between">
          <span className="truncate">{value}</span>
          <ChevronDownIcon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent>
        {loading ? (
          <div className="flex justify-center p-2">
            <Spinner />
          </div>
        ) : (
          <DropdownMenuRadioGroup value={value} onValueChange={onSelect}>
            {values.map((other) => (
              <DropdownMenuRadioItem key={other} value={other}>
                {other}
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function Field({
  label,
  htmlFor,
  ui,
  children,
}: {
  label: string;
  htmlFor: string;
  ui: boolean;
  children: React.ReactNode;
}) {
  return (
    <div className="grid grid-cols-[4rem_1fr] items-center gap-2">
      <Label htmlFor={htmlFor}>{label}</Label>
      <div className="flex min-w-0 items-center gap-2">
        {children}
        {ui && <UiBadge />}
      </div>
    </div>
  );
}

function RoleSheet({
  role,
  harnesses,
  onChange,
  onClose,
}: {
  role: Role;
  harnesses: HarnessChoices[] | undefined;
  onChange: (role: Role) => void;
  onClose: () => void;
}) {
  const showLogin = use(LoginContext);
  const [harness, setHarness] = useState(role.harness);
  const [model, setModel] = useState(role.model);
  const [effort, setEffort] = useState(role.effort);
  const [max, setMax] = useState(String(role.max));
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);

  const choices = harnesses?.find((other) => other.harness === harness);
  const models = choices?.models ?? [];
  const chosen = models.find((other) => other.model === model);
  const efforts = chosen ? chosen.efforts : effort ? [effort] : [];
  const changed = Object.values(role.ui).some(Boolean);

  const pickHarness = (value: string) => {
    if (value === harness) return;
    const first = harnesses?.find((other) => other.harness === value)?.models[0];
    setHarness(value);
    setModel(first?.model ?? "");
    setEffort(keepEffort(effort, first?.efforts ?? []));
  };

  const pickModel = (value: string) => {
    setModel(value);
    setEffort(keepEffort(effort, models.find((other) => other.model === value)?.efforts ?? []));
  };

  const submit = async (send: () => Promise<setRoleResponse | resetRoleResponse>) => {
    setSaving(true);
    setError("");
    try {
      const res = await send();
      if (res.status === 200) {
        onChange(res.data.data);
        return;
      }
      if (res.status === 401) {
        showLogin();
      } else {
        setError(res.data.error);
      }
    } catch (err) {
      setError(String(err));
    }
    setSaving(false);
  };

  const name = role.role as Parameters<typeof setRole>[0];

  return (
    <Sheet open onOpenChange={(open) => !open && onClose()}>
      <SheetContent
        side="bottom"
        aria-describedby={undefined}
        className="gap-3 p-4 pt-3 md:mx-auto md:max-w-md md:rounded-t-xl"
      >
        <SheetTitle>{roleName(role)}</SheetTitle>
        <Field label="Harness" htmlFor="role-harness" ui={role.ui.harness}>
          <Choice
            id="role-harness"
            value={harness}
            values={harnesses?.map((other) => other.harness) ?? []}
            loading={!harnesses}
            onSelect={pickHarness}
          />
        </Field>
        <Field label="Model" htmlFor="role-model" ui={role.ui.model}>
          <Choice
            id="role-model"
            value={model}
            values={models.map((other) => other.model)}
            loading={!harnesses}
            onSelect={pickModel}
          />
        </Field>
        {efforts.length > 0 && (
          <Field label="Effort" htmlFor="role-effort" ui={role.ui.effort}>
            <Choice
              id="role-effort"
              value={effort}
              values={efforts}
              loading={!harnesses}
              onSelect={setEffort}
            />
          </Field>
        )}
        <Field label="Max" htmlFor="role-max" ui={role.ui.max}>
          <Input
            id="role-max"
            type="number"
            min={1}
            value={max}
            onChange={(event) => setMax(event.target.value)}
          />
        </Field>
        {choices?.error && (
          <Badge variant="destructive" className="h-auto whitespace-normal">
            {choices.error}
          </Badge>
        )}
        {error && (
          <Badge variant="destructive" className="h-auto whitespace-normal">
            {error}
          </Badge>
        )}
        <div className="flex items-center justify-between gap-2">
          {changed ? (
            <Button
              variant="outline"
              disabled={saving}
              onClick={() => void submit(() => resetRole(name))}
            >
              Reset
            </Button>
          ) : (
            <span />
          )}
          <Button
            disabled={!harnesses || saving}
            onClick={() =>
              void submit(() =>
                setRole(name, {
                  harness,
                  model,
                  effort: efforts.length > 0 ? effort : "",
                  max: Number(max),
                }),
              )
            }
          >
            {saving && <Spinner />}
            Save
          </Button>
        </div>
      </SheetContent>
    </Sheet>
  );
}

export function Roles() {
  const showLogin = use(LoginContext);
  const [roles, setRoles] = useState<Role[]>();
  const [harnesses, setHarnesses] = useState<HarnessChoices[]>();
  const [error, setError] = useState("");
  const [selected, setSelected] = useState("");

  useEffect(() => {
    listRoles()
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 200) {
          setRoles(res.data.data);
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)));
    listHarnesses()
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 200) {
          setHarnesses(res.data.data);
        } else {
          setHarnesses([]);
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => {
        setHarnesses([]);
        setError(String(err));
      });
  }, [showLogin]);

  const role = roles?.find((other) => other.role === selected);

  return (
    <>
      <TopBar title="Roles" back="/settings" />
      <PageHeader title="Roles" back="/settings" />
      {error && <ErrorBadge>{error}</ErrorBadge>}
      <List>
        {roles?.map((other) => (
          <RoleRow key={other.role} role={other} onOpen={() => setSelected(other.role)} />
        ))}
      </List>
      {role && (
        <RoleSheet
          key={role.role}
          role={role}
          harnesses={harnesses}
          onChange={(updated) => {
            setRoles((current) =>
              current?.map((other) => (other.role === updated.role ? updated : other)),
            );
            setSelected((current) => (current === updated.role ? "" : current));
          }}
          onClose={() => setSelected("")}
        />
      )}
    </>
  );
}
