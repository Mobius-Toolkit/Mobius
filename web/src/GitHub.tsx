import { use, useState, type FormEvent } from "react";
import { createManifestForm, type GitHubApp, type ManifestForm } from "@/api/api.gen";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { LoginContext } from "@/lib/login";
import { BackButton } from "./BackButton";

function postToGitHub(form: ManifestForm) {
  const element = document.createElement("form");
  element.method = "post";
  element.action = form.url;
  const field = document.createElement("input");
  field.type = "hidden";
  field.name = "manifest";
  field.value = form.manifest;
  element.append(field);
  document.body.append(element);
  element.submit();
}

export function GitHub({ apps, back }: { apps: GitHubApp[]; back?: boolean }) {
  const showLogin = use(LoginContext);
  const [account, setAccount] = useState("");
  const [name, setName] = useState("");
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    createManifestForm({ account, name, origin: window.location.origin })
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 200) {
          postToGitHub(res.data.data);
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)))
      .finally(() => setBusy(false));
  };

  return (
    <Card className="w-full max-w-lg">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          {back && <BackButton parent="/settings" />}
          Connect GitHub
        </CardTitle>
        {apps.length > 0 && (
          <CardDescription>
            Install each App on the repositories of its organization.
          </CardDescription>
        )}
      </CardHeader>
      <CardContent className="grid gap-6">
        {apps.length > 0 && (
          <ul className="grid gap-4">
            {apps.map((app) => (
              <li key={app.slug} className="grid gap-1">
                <a href={app.installUrl} className="font-medium hover:underline">
                  Install {app.slug} on your repositories
                </a>
                <span className="text-muted-foreground">
                  {app.repositories.length === 0 ? "No repositories" : app.repositories.join(", ")}
                </span>
              </li>
            ))}
          </ul>
        )}
        <form onSubmit={submit}>
          <FieldGroup>
            {apps.length > 0 && <h3 className="font-medium">Add an organization</h3>}
            <Field>
              <FieldLabel htmlFor="account">Account or organization</FieldLabel>
              <Input
                id="account"
                required
                value={account}
                onChange={(e) => setAccount(e.target.value)}
              />
            </Field>
            <Field data-invalid={error !== undefined}>
              <FieldLabel htmlFor="name">App name</FieldLabel>
              <Input
                id="name"
                placeholder={`Mobius ${account}`}
                value={name}
                onChange={(e) => setName(e.target.value)}
                aria-invalid={error !== undefined}
              />
              <FieldDescription>
                GitHub App names are unique on all of GitHub. Use a name that no other App has, for
                example with your account name.
              </FieldDescription>
              <FieldError>{error}</FieldError>
            </Field>
            <Button type="submit" disabled={busy}>
              Create the App
            </Button>
          </FieldGroup>
        </form>
      </CardContent>
    </Card>
  );
}
