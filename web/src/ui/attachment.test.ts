import * as Scene from "foldkit/scene";
import { describe, it } from "vitest";
import { renderAttachmentCard } from "./attachment";
import { messageSquareQuoteIcon } from "./icons";

type Options = Omit<
  Parameters<typeof renderAttachmentCard<string>>[1],
  "kind" | "icon" | "header" | "removeLabel"
>;

const card = (options: Options) => ({
  update: (_model: string, message: string) => ({ model: message }),
  view: (model: string, h: Parameters<typeof renderAttachmentCard<string>>[0]) =>
    h.div(
      [],
      [
        renderAttachmentCard(h, {
          kind: "selection",
          icon: messageSquareQuoteIcon(h),
          header: "Lead message",
          removeLabel: "Remove the selection",
          ...options,
        }),
        h.p([h.Id("sent")], [model]),
      ],
    ),
});

const root = Scene.selector("[data-attachment-card]");

describe("renderAttachmentCard", () => {
  it("is a div with no onClick and no href", () => {
    Scene.scene(
      card({}),
      Scene.given(""),
      Scene.expect(root).toExist(),
      Scene.expect(Scene.selector("div[data-attachment-card='selection']")).toExist(),
      Scene.expect(Scene.role("button")).toBeAbsent(),
      Scene.expect(Scene.role("link")).toBeAbsent(),
    );
  });

  it("is a button with onClick that sends the Message", () => {
    Scene.scene(
      card({ onClick: "opened" }),
      Scene.given(""),
      Scene.expect(Scene.selector("button[data-attachment-card='selection']")).toHaveClass(
        "hover:bg-accent",
      ),
      Scene.click(root),
      Scene.expect(Scene.selector("#sent")).toHaveText("opened"),
    );
  });

  it("is a link in a new tab with href", () => {
    Scene.scene(
      card({ href: "https://example.com/item" }),
      Scene.given(""),
      Scene.expect(Scene.selector("a[data-attachment-card='selection']")).toHaveAttr(
        "href",
        "https://example.com/item",
      ),
      Scene.expect(root).toHaveAttr("target", "_blank"),
      Scene.expect(root).toHaveAttr("rel", "noreferrer"),
      Scene.expect(root).toHaveClass("hover:bg-accent"),
    );
  });

  it("shows the remove button with its label only with onRemove", () => {
    Scene.scene(
      card({ onRemove: "removed" }),
      Scene.given(""),
      Scene.expect(Scene.role("button", { name: "Remove the selection" })).toHaveAttr(
        "title",
        "Remove the selection",
      ),
      Scene.click(Scene.role("button", { name: "Remove the selection" })),
      Scene.expect(Scene.selector("#sent")).toHaveText("removed"),
    );
    Scene.scene(
      card({}),
      Scene.given(""),
      Scene.expect(Scene.role("button", { name: "Remove the selection" })).toBeAbsent(),
    );
  });
});
