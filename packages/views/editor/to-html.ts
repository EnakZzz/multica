// Some local installs still resolve hast-util-to-html 4.x, which does not ship
// declarations, while clean Docker installs resolve 9.x. Keep the dependency
// isolated behind the narrow API we use.
// @ts-ignore
import { toHtml as importedToHtml } from "hast-util-to-html";

export interface ToHtmlOptions {
  allowDangerousHtml?: boolean | null | undefined;
}

const hastToHtml = importedToHtml as (tree: unknown, options?: ToHtmlOptions) => string;

export function toHtml(tree: unknown, options?: ToHtmlOptions): string {
  return hastToHtml(tree, options);
}
