import type { AnnotationPoint, AnnotationRect, ReviewAnnotation, ReviewCapture } from "./shared/types";

const ROOT_ID = "multica-review-root";
let annotations: ReviewAnnotation[] = [];
let root: HTMLDivElement | null = null;
let shadow: ShadowRoot | null = null;
let drawing = false;
let currentStroke: AnnotationPoint[] = [];
let currentPath: SVGPathElement | null = null;

function createContentId(): string {
  const bytes = new Uint8Array(16);
  if (typeof crypto !== "undefined" && typeof crypto.getRandomValues === "function") {
    crypto.getRandomValues(bytes);
  } else {
    for (let i = 0; i < bytes.length; i += 1) {
      bytes[i] = Math.floor(Math.random() * 256);
    }
  }

  bytes[6] = (bytes[6]! & 0x0f) | 0x40;
  bytes[8] = (bytes[8]! & 0x3f) | 0x80;
  const hex = [...bytes].map((byte) => byte.toString(16).padStart(2, "0"));
  return [
    hex.slice(0, 4).join(""),
    hex.slice(4, 6).join(""),
    hex.slice(6, 8).join(""),
    hex.slice(8, 10).join(""),
    hex.slice(10, 16).join(""),
  ].join("-");
}

function injectStyles(target: ShadowRoot) {
  const style = document.createElement("style");
  style.textContent = `
    :host { all: initial; }
    .overlay { position: fixed; inset: 0; z-index: 2147483647; cursor: crosshair; font-family: Inter, ui-sans-serif, system-ui, sans-serif; color: #111827; }
    .shade { position: absolute; inset: 0; background: rgba(17, 24, 39, 0.08); pointer-events: none; }
    .ink { position: fixed; inset: 0; width: 100vw; height: 100vh; pointer-events: none; overflow: visible; }
    .toolbar, .panel { position: fixed; background: #fff; border: 1px solid rgba(17, 24, 39, 0.14); box-shadow: 0 18px 50px rgba(17, 24, 39, 0.22); border-radius: 8px; cursor: default; }
    .toolbar { left: 16px; top: 16px; display: flex; align-items: center; gap: 8px; padding: 8px 10px; }
    .toolbar strong { font-size: 13px; }
    .toolbar span { font-size: 12px; color: #6b7280; }
    .panel { right: 16px; top: 16px; width: min(360px, calc(100vw - 32px)); max-height: calc(100vh - 32px); overflow: auto; padding: 12px; }
    .panel h2 { font-size: 14px; margin: 0 0 8px; }
    .panel p { font-size: 12px; color: #6b7280; margin: 0 0 12px; line-height: 1.4; }
    .badge { position: fixed; min-width: 20px; height: 20px; box-sizing: border-box; background: #2563eb; color: #fff; font-size: 12px; line-height: 20px; text-align: center; padding: 0 6px; border-radius: 999px; pointer-events: none; }
    label { display: block; font-size: 12px; color: #374151; margin: 8px 0 4px; }
    textarea { box-sizing: border-box; width: 100%; min-height: 76px; resize: vertical; border: 1px solid #d1d5db; border-radius: 6px; padding: 8px; font: 13px/1.4 ui-sans-serif, system-ui, sans-serif; color: #111827; background: #fff; }
    .annotation { border-top: 1px solid #e5e7eb; padding-top: 10px; margin-top: 10px; }
    .row { display: flex; gap: 8px; align-items: center; justify-content: space-between; }
    button { border: 1px solid #d1d5db; background: #fff; border-radius: 6px; padding: 6px 10px; font: 12px ui-sans-serif, system-ui, sans-serif; color: #111827; cursor: pointer; }
    button.primary { background: #111827; color: #fff; border-color: #111827; }
    button.danger { color: #b91c1c; }
    button:disabled { opacity: 0.55; cursor: not-allowed; }
    .status { font-size: 12px; color: #4b5563; margin-top: 8px; }
  `;
  target.append(style);
}

function pathFromPoints(points: AnnotationPoint[]): string {
  if (points.length === 0) return "";
  const first = points[0]!;
  const rest = points.slice(1);
  return `M ${first.x} ${first.y} ${rest.map((point) => `L ${point.x} ${point.y}`).join(" ")}`;
}

function rectFromPoints(points: AnnotationPoint[]): AnnotationRect | undefined {
  if (points.length < 2) return undefined;
  const xs = points.map((point) => point.x);
  const ys = points.map((point) => point.y);
  const x = Math.min(...xs);
  const y = Math.min(...ys);
  const width = Math.max(...xs) - x;
  const height = Math.max(...ys) - y;
  if (width < 8 && height < 8) return undefined;
  return { x, y, width, height };
}

function summarizeElementAt(x: number, y: number): string {
  const previousDisplay = root?.style.display;
  if (root) root.style.display = "none";
  const el = document.elementFromPoint(x, y);
  if (root) root.style.display = previousDisplay ?? "";
  if (!el) return "";
  const name = el.tagName.toLowerCase();
  const id = el.id ? `#${el.id}` : "";
  const cls = typeof el.className === "string" && el.className.trim()
    ? `.${el.className.trim().split(/\s+/).slice(0, 3).join(".")}`
    : "";
  const text = (el.textContent ?? "").replace(/\s+/g, " ").trim().slice(0, 120);
  return [name + id + cls, text].filter(Boolean).join(" - ");
}

function svgEl(tag: string) {
  return document.createElementNS("http://www.w3.org/2000/svg", tag);
}

function renderInk() {
  if (!shadow) return;
  const ink = shadow.querySelector<SVGSVGElement>(".ink");
  const overlay = shadow.querySelector<HTMLElement>(".overlay");
  if (!ink || !overlay) return;
  ink.replaceChildren();
  overlay.querySelectorAll(".badge").forEach((item) => item.remove());
  annotations.forEach((annotation, index) => {
    for (const stroke of annotation.strokes ?? []) {
      const path = svgEl("path") as SVGPathElement;
      path.setAttribute("d", pathFromPoints(stroke));
      path.setAttribute("fill", "none");
      path.setAttribute("stroke", "#2563eb");
      path.setAttribute("stroke-width", "4");
      path.setAttribute("stroke-linecap", "round");
      path.setAttribute("stroke-linejoin", "round");
      ink.append(path);
    }
    if (annotation.rect) {
      const badge = document.createElement("div");
      badge.className = "badge";
      badge.textContent = String(index + 1);
      badge.style.left = `${annotation.rect.x}px`;
      badge.style.top = `${Math.max(6, annotation.rect.y - 24)}px`;
      overlay.append(badge);
    }
  });
}

function addPageNote() {
  annotations.push({
    id: createContentId(),
    kind: "note",
    note: "",
    targetSummary: "page-level note",
  });
  renderPanel();
}

function renderPanel() {
  if (!shadow) return;
  const panel = shadow.querySelector<HTMLElement>(".panel");
  if (!panel) return;
  renderInk();
  panel.innerHTML = `
    <h2>Multica review</h2>
    <p>Use the pencil to circle a problem area, then describe the issue. Or add a page note without drawing.</p>
    <div class="row">
      <button class="add-note">Add page note</button>
      <button class="clear">Clear all</button>
    </div>
    <div class="list"></div>
    <div class="row" style="margin-top:12px;">
      <button class="close">Cancel</button>
      <button class="primary submit" ${annotations.length === 0 ? "disabled" : ""}>Submit issue</button>
    </div>
    <div class="status"></div>
  `;
  const list = panel.querySelector<HTMLElement>(".list");
  annotations.forEach((annotation, index) => {
    const item = document.createElement("div");
    item.className = "annotation";
    item.innerHTML = `
      <div class="row">
        <strong>${annotation.kind === "note" ? "Page note" : `Pencil mark ${index + 1}`}</strong>
        <button class="danger" data-delete="${annotation.id}">Delete</button>
      </div>
      <label>Issue description</label>
      <textarea data-note="${annotation.id}" placeholder="Describe what should change..."></textarea>
    `;
    const textarea = item.querySelector<HTMLTextAreaElement>("textarea");
    if (textarea) textarea.value = annotation.note;
    list?.append(item);
  });
  panel.querySelector(".add-note")?.addEventListener("click", addPageNote);
  panel.querySelector(".clear")?.addEventListener("click", () => {
    annotations = [];
    renderPanel();
  });
  panel.querySelector(".close")?.addEventListener("click", cleanup);
  panel.querySelector(".submit")?.addEventListener("click", () => void submitCapture());
  panel.querySelectorAll<HTMLButtonElement>("[data-delete]").forEach((button) => {
    button.addEventListener("click", () => {
      annotations = annotations.filter((item) => item.id !== button.dataset.delete);
      renderPanel();
    });
  });
  panel.querySelectorAll<HTMLTextAreaElement>("[data-note]").forEach((textarea) => {
    textarea.addEventListener("input", () => {
      const annotation = annotations.find((item) => item.id === textarea.dataset.note);
      if (annotation) annotation.note = textarea.value;
    });
  });
  panel.querySelector<HTMLTextAreaElement>("textarea:last-of-type")?.focus();
}

function visibleTextSummary(): string[] {
  const values: string[] = [];
  try {
    const selectors = "h1,h2,h3,button,a,label,[role='button'],input,textarea";
    document.querySelectorAll<HTMLElement>(selectors).forEach((el) => {
      const rect = el.getBoundingClientRect();
      if (rect.bottom < 0 || rect.top > window.innerHeight || rect.right < 0 || rect.left > window.innerWidth) return;
      const text = (el.innerText || el.getAttribute("aria-label") || el.getAttribute("placeholder") || "").replace(/\s+/g, " ").trim();
      if (text && !values.includes(text)) values.push(text.slice(0, 160));
    });
  } catch {
    return [];
  }
  return values.slice(0, 30);
}

function safeDomSummary(): ReviewCapture["domSummary"] {
  try {
    return {
      activeElement: document.activeElement ? summarizeElementAt(window.innerWidth / 2, window.innerHeight / 2) : undefined,
      selectedText: String(window.getSelection()?.toString() ?? "").slice(0, 500),
      visibleText: visibleTextSummary(),
    };
  } catch {
    return { visibleText: [] };
  }
}

function buildCapture(): ReviewCapture {
  return {
    url: location.href,
    title: document.title,
    capturedAt: new Date().toISOString(),
    viewport: {
      width: window.innerWidth,
      height: window.innerHeight,
      scrollX: window.scrollX,
      scrollY: window.scrollY,
      devicePixelRatio: window.devicePixelRatio,
    },
    annotations,
    domSummary: safeDomSummary(),
  };
}

async function submitCapture() {
  if (!shadow) return;
  const status = shadow.querySelector<HTMLElement>(".status");
  const submit = shadow.querySelector<HTMLButtonElement>(".submit");
  if (status) status.textContent = "Submitting to Multica...";
  if (submit) submit.disabled = true;
  try {
    const response = await chrome.runtime.sendMessage({ type: "SUBMIT_REVIEW", capture: buildCapture() }) as {
      ok?: boolean;
      response?: { issueUrl: string; issueIdentifier?: string };
      error?: string;
    };
    if (!response.ok) throw new Error(response.error ?? "Submit failed");
    if (status) {
      const label = response.response?.issueIdentifier || "issue";
      status.innerHTML = `Created ${label}. <button class="open">Open</button>`;
      status.querySelector(".open")?.addEventListener("click", () => {
        window.open(response.response?.issueUrl ?? location.href, "_blank", "noopener,noreferrer");
      });
    }
  } catch (err) {
    if (status) status.textContent = err instanceof Error ? err.message : "Submit failed";
    if (submit) submit.disabled = false;
  }
}

function cleanup() {
  root?.remove();
  root = null;
  shadow = null;
  annotations = [];
  drawing = false;
  currentStroke = [];
  currentPath = null;
}

function startStroke(event: MouseEvent, ink: SVGSVGElement) {
  if ((event.target as HTMLElement).closest(".panel,.toolbar")) return;
  drawing = true;
  currentStroke = [{ x: event.clientX, y: event.clientY }];
  currentPath = svgEl("path") as SVGPathElement;
  currentPath.setAttribute("fill", "none");
  currentPath.setAttribute("stroke", "#2563eb");
  currentPath.setAttribute("stroke-width", "4");
  currentPath.setAttribute("stroke-linecap", "round");
  currentPath.setAttribute("stroke-linejoin", "round");
  ink.append(currentPath);
}

function moveStroke(event: MouseEvent) {
  if (!drawing || !currentPath) return;
  currentStroke.push({ x: event.clientX, y: event.clientY });
  currentPath.setAttribute("d", pathFromPoints(currentStroke));
}

function finishStroke() {
  if (!drawing) return;
  drawing = false;
  currentPath?.remove();
  currentPath = null;
  const rect = rectFromPoints(currentStroke);
  if (!rect) {
    currentStroke = [];
    return;
  }
  annotations.push({
    id: createContentId(),
    kind: "pencil",
    rect,
    pageRect: { ...rect, x: rect.x + window.scrollX, y: rect.y + window.scrollY },
    strokes: [currentStroke],
    note: "",
    targetSummary: summarizeElementAt(rect.x + rect.width / 2, rect.y + rect.height / 2),
  });
  currentStroke = [];
  renderPanel();
}

function start() {
  cleanup();
  root = document.createElement("div");
  root.id = ROOT_ID;
  document.documentElement.append(root);
  shadow = root.attachShadow({ mode: "open" });
  injectStyles(shadow);
  const overlay = document.createElement("div");
  overlay.className = "overlay";
  overlay.innerHTML = `
    <div class="shade"></div>
    <svg class="ink"></svg>
    <div class="toolbar"><strong>Multica review</strong><span>Pencil: circle a problem area · Esc to close</span></div>
    <div class="panel"></div>
  `;
  shadow.append(overlay);
  const ink = shadow.querySelector<SVGSVGElement>(".ink");
  if (!ink) return;
  renderPanel();
  overlay.addEventListener("mousedown", (event) => startStroke(event, ink));
  overlay.addEventListener("mousemove", moveStroke);
  overlay.addEventListener("mouseup", finishStroke);
  overlay.addEventListener("mouseleave", finishStroke);
}

document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && root) cleanup();
});

chrome.runtime.onMessage.addListener((message: unknown) => {
  const msg = message as { type?: string };
  if (msg.type === "MULTICA_REVIEW_START") start();
});
