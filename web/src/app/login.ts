import type { Html, HtmlBuilder } from "foldkit/html";
import { renderButton } from "@/ui/button";
import { Message } from "./message";
import type { Model } from "./model";

export function renderLogin(h: HtmlBuilder<Message>, model: Model): Html {
  const hasError = Boolean(model.loginError);

  return h.main(
    [h.Class("flex min-h-svh items-center justify-center p-6")],
    [
      h.div(
        [
          h.DataAttribute("slot", "card"),
          h.Class("w-full max-w-sm rounded-xl border bg-card text-card-foreground shadow-sm"),
        ],
        [
          h.div(
            [h.DataAttribute("slot", "card-header"), h.Class("flex flex-col gap-1.5 p-6")],
            [
              h.div(
                [
                  h.DataAttribute("slot", "card-title"),
                  h.Class("leading-none font-semibold tracking-tight"),
                ],
                ["Mobius"],
              ),
              h.div(
                [
                  h.DataAttribute("slot", "card-description"),
                  h.Class("text-sm text-muted-foreground"),
                ],
                ["Log in with the access password."],
              ),
            ],
          ),
          h.div(
            [h.DataAttribute("slot", "card-content"), h.Class("p-6 pt-0")],
            [
              h.form(
                [h.OnSubmit(Message.SubmitLoginRequested())],
                [
                  h.div(
                    [h.DataAttribute("slot", "field-group"), h.Class("grid gap-4")],
                    [
                      h.div(
                        [
                          h.DataAttribute("slot", "field"),
                          ...(hasError ? [h.DataAttribute("invalid", "")] : []),
                          h.Class("grid gap-2"),
                        ],
                        [
                          h.label(
                            [
                              h.Attribute("for", "password"),
                              h.Class(
                                "text-sm font-medium leading-none peer-disabled:cursor-not-allowed peer-disabled:opacity-70",
                              ),
                            ],
                            ["Access password"],
                          ),
                          h.input([
                            h.Id("password"),
                            h.Type("password"),
                            h.Attribute("autocomplete", "current-password"),
                            h.Attribute("autofocus", ""),
                            h.Attribute("required", ""),
                            h.Value(model.passwordInput),
                            h.OnInput((val) => Message.PasswordInputChanged({ password: val })),
                            ...(hasError ? [h.AriaInvalid(true)] : []),
                            h.Class(
                              "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs transition-colors file:border-0 file:bg-transparent file:text-sm file:font-medium placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50",
                            ),
                          ]),
                          hasError
                            ? h.p(
                                [
                                  h.DataAttribute("slot", "field-error"),
                                  h.Class("text-xs text-destructive font-medium"),
                                ],
                                [model.loginError],
                              )
                            : h.empty,
                        ],
                      ),
                      renderButton(h, {
                        label: "Log in",
                        pending: model.loginBusy,
                        onClick: Message.SubmitLoginRequested(),
                      }),
                    ],
                  ),
                ],
              ),
            ],
          ),
        ],
      ),
    ],
  );
}
