// hast-util-to-html 4.x does not ship TypeScript declarations. Keep the
// untyped dependency isolated here so apps type-checking @multica/views source
// do not need their own ambient module declarations.
// @ts-ignore
import { toHtml as hastToHtml } from "hast-util-to-html";

export interface ToHtmlOptions {
  allowDangerousHtml?: boolean;
}

export function toHtml(tree: unknown, options?: ToHtmlOptions): string {
  return hastToHtml(tree, options);
}
