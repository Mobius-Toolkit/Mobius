import { use, useEffect, useState } from "react";
import { listHarnesses, type HarnessChoices, type IssueSetting } from "@/api/api.gen";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetDescription, SheetTitle } from "@/components/ui/sheet";
import { Spinner } from "@/components/ui/spinner";
import { bindingText, keepEffort, type Reply } from "@/lib/harness";
import { LoginContext } from "@/lib/login";
import { showNotice } from "@/lib/notice";
import { Choice, Field } from "./Choice";

// own is the setting that the Owner made, or null. fallback is the binding that applies with no setting.
export type SettingTarget = {
  title: string;
  own: IssueSetting | null;
  fallback: IssueSetting;
  fallbackSource: "Workstream" | "role";
  save: (setting: IssueSetting) => Promise<Reply>;
  clear: () => Promise<Reply>;
};

export function SettingSheet({
  target,
  onChange,
  onClose,
}: {
  target: SettingTarget;
  onChange: (setting: IssueSetting | null) => void;
  onClose: () => void;
}) {
  const showLogin = use(LoginContext);
  const start = target.own ?? target.fallback;
  const [harnesses, setHarnesses] = useState<HarnessChoices[]>();
  const [harness, setHarness] = useState(start.harness);
  const [model, setModel] = useState(start.model);
  const [effort, setEffort] = useState(start.effort);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
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

  const choices = harnesses?.find((other) => other.harness === harness);
  const models = choices?.models ?? [];
  const chosen = models.find((other) => other.model === model);
  const efforts = chosen ? chosen.efforts : effort ? [effort] : [];

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

  const submit = async (send: () => Promise<Reply>, done: () => void) => {
    setSaving(true);
    setError("");
    try {
      const res = await send();
      if (res.status === 204) {
        done();
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

  const save = () => {
    const setting = { harness, model, effort: efforts.length > 0 ? effort : "" };
    void submit(
      () => target.save(setting),
      () => {
        onChange(setting);
        showNotice(`Set the ${target.title} to ${bindingText(setting)}`);
      },
    );
  };

  const clear = () =>
    void submit(target.clear, () => {
      onChange(null);
      showNotice(`Cleared the setting of the ${target.title}`);
    });

  return (
    <Sheet open onOpenChange={(open) => !open && onClose()}>
      <SheetContent side="bottom" className="gap-3 p-4 pt-3 md:mx-auto md:max-w-md md:rounded-t-xl">
        <SheetTitle>{target.title}</SheetTitle>
        <SheetDescription>
          Default: {bindingText(target.fallback)} ({target.fallbackSource})
        </SheetDescription>
        <Field label="Harness" htmlFor="setting-harness">
          <Choice
            id="setting-harness"
            value={harness}
            values={harnesses?.map((other) => other.harness) ?? []}
            loading={!harnesses}
            onSelect={pickHarness}
          />
        </Field>
        <Field label="Model" htmlFor="setting-model">
          <Choice
            id="setting-model"
            value={model}
            values={models.map((other) => other.model)}
            loading={!harnesses}
            onSelect={pickModel}
          />
        </Field>
        {efforts.length > 0 && (
          <Field label="Effort" htmlFor="setting-effort">
            <Choice
              id="setting-effort"
              value={effort}
              values={efforts}
              loading={!harnesses}
              onSelect={setEffort}
            />
          </Field>
        )}
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
          {target.own ? (
            <Button variant="outline" disabled={saving} onClick={clear}>
              Use the default
            </Button>
          ) : (
            <span />
          )}
          <Button disabled={!harnesses || saving} onClick={save}>
            {saving && <Spinner />}
            Save
          </Button>
        </div>
      </SheetContent>
    </Sheet>
  );
}
