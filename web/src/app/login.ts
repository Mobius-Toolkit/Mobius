import type { Html, HtmlBuilder } from "foldkit/html";
import { renderButton } from "@/ui/button";
import { inputClass } from "@/ui/input";
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
          h.DataAttribute("size", "default"),
          h.Class(
            "group/card flex w-full max-w-sm flex-col gap-(--card-spacing) overflow-hidden rounded-xl bg-card py-(--card-spacing) text-sm text-card-foreground ring-1 ring-foreground/10 [--card-spacing:--spacing(4)]",
          ),
        ],
        [
          h.div(
            [
              h.DataAttribute("slot", "card-header"),
              h.Class(
                "group/card-header @container/card-header grid auto-rows-min items-start gap-1 rounded-t-xl px-(--card-spacing) has-data-[slot=card-description]:grid-rows-[auto_auto]",
              ),
            ],
            [
              h.div(
                [
                  h.DataAttribute("slot", "card-title"),
                  h.Class("font-heading text-base leading-snug font-medium"),
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
            [h.DataAttribute("slot", "card-content"), h.Class("px-(--card-spacing)")],
            [
              h.form(
                [h.OnSubmit(Message.SubmitLoginRequested())],
                [
                  h.div(
                    [
                      h.DataAttribute("slot", "field-group"),
                      h.Class(
                        "group/field-group @container/field-group flex w-full flex-col gap-5",
                      ),
                    ],
                    [
                      h.div(
                        [
                          h.Role("group"),
                          h.DataAttribute("slot", "field"),
                          h.DataAttribute("orientation", "vertical"),
                          h.DataAttribute("invalid", String(hasError)),
                          h.Class(
                            "group/field flex w-full flex-col gap-2 data-[invalid=true]:text-destructive *:w-full [&>.sr-only]:w-auto",
                          ),
                        ],
                        [
                          h.label(
                            [
                              h.Attribute("for", "password"),
                              h.DataAttribute("slot", "field-label"),
                              h.Class(
                                "group/field-label peer/field-label flex w-fit items-center gap-2 text-sm leading-snug font-medium select-none",
                              ),
                            ],
                            ["Access password"],
                          ),
                          h.input([
                            h.Id("password"),
                            h.Type("password"),
                            h.DataAttribute("slot", "input"),
                            h.Attribute("autocomplete", "current-password"),
                            h.Attribute("autofocus", ""),
                            h.Attribute("required", ""),
                            h.Value(model.passwordInput),
                            h.OnInput((val) => Message.PasswordInputChanged({ password: val })),
                            h.AriaInvalid(hasError),
                            h.Class(inputClass()),
                          ]),
                          hasError
                            ? h.div(
                                [
                                  h.Role("alert"),
                                  h.DataAttribute("slot", "field-error"),
                                  h.Class("text-sm font-normal text-destructive"),
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
