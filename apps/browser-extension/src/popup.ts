import { mountReviewExtensionUi } from "./ui/app";

void mountReviewExtensionUi({
  serverUrl: document.querySelector<HTMLInputElement>("#server-url")!,
  workspace: document.querySelector<HTMLSelectElement>("#workspace")!,
  project: document.querySelector<HTMLSelectElement>("#project")!,
  status: document.querySelector<HTMLElement>("#status")!,
  login: document.querySelector<HTMLButtonElement>("#login")!,
  refresh: document.querySelector<HTMLButtonElement>("#refresh")!,
  start: document.querySelector<HTMLButtonElement>("#start")!,
  logout: document.querySelector<HTMLButtonElement>("#logout")!,
});
