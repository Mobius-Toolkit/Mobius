const focusable =
  'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])';

export function trapTab(event: KeyboardEvent, dialog: HTMLElement) {
  const items = [...dialog.querySelectorAll<HTMLElement>(focusable)];
  const first = items[0];
  const last = items[items.length - 1];
  if (!first || !dialog.contains(document.activeElement)) {
    event.preventDefault();
    (first ?? dialog).focus();
  } else if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}
