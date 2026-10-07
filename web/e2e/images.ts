import type { Page } from "@playwright/test";

// The page draws the PNG, because the tests have no image library. The image has one color, so each run gives the
// same bytes.
export async function png(page: Page, width: number, height: number, color: string) {
  const data = (await page.evaluate(`(() => {
    const canvas = document.createElement('canvas')
    canvas.width = ${width}
    canvas.height = ${height}
    const context = canvas.getContext('2d')
    context.fillStyle = ${JSON.stringify(color)}
    context.fillRect(0, 0, ${width}, ${height})
    return canvas.toDataURL('image/png').split(',')[1]
  })()`)) as string;
  return Buffer.from(data, "base64");
}

// Resolves to false when the page cancels the paste.
export function pasteImage(page: Page, image: Buffer, text = "") {
  return page.evaluate<boolean>(`(() => {
    const bytes = Uint8Array.from(atob(${JSON.stringify(image.toString("base64"))}), (char) => char.charCodeAt(0))
    const data = new DataTransfer()
    data.items.add(new File([bytes], 'image.png', { type: 'image/png' }))
    if (${JSON.stringify(text)}) {
      data.setData('text/plain', ${JSON.stringify(text)})
    }
    return document.querySelector('main form textarea').dispatchEvent(
      new ClipboardEvent('paste', { clipboardData: data, bubbles: true, cancelable: true }),
    )
  })()`);
}
