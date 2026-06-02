const statusEl = document.querySelector<HTMLElement>("#status");

function setStatus(message: string, error = false) {
  if (!statusEl) return;
  statusEl.textContent = message;
  statusEl.classList.toggle("error", error);
}

async function completeAuth() {
  const params = new URL(location.href).searchParams;
  const token = params.get("token") ?? "";
  const state = params.get("state") ?? "";
  if (!token || !state) {
    throw new Error("The Multica callback did not include a token.");
  }
  const response = await chrome.runtime.sendMessage({
    type: "AUTH_CALLBACK",
    token,
    state,
  }) as { ok?: boolean; error?: string };
  if (!response.ok) {
    throw new Error(response.error ?? "The Multica callback could not be verified.");
  }
  setStatus("Signed in. You can close this tab and return to the extension.");
}

completeAuth().catch((error: unknown) => {
  setStatus(error instanceof Error ? error.message : "Sign in failed.", true);
});

export {};
