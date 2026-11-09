import type { Locator } from "@playwright/test";

export function buttonNames(row: Locator) {
  return row
    .getByRole("button")
    .evaluateAll((buttons) =>
      buttons.map((button) => button.getAttribute("aria-label") ?? button.textContent),
    );
}
