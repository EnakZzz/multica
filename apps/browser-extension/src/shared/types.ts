export interface StoredWorkspace {
  id: string;
  slug: string;
  name: string;
}

export interface StoredProject {
  id: string;
  name: string;
  title?: string;
  slug?: string;
  status?: string;
}

export interface RecentSelection {
  workspaceSlug: string;
  workspaceName: string;
  projectId: string;
  projectName: string;
  usedAt: string;
}

export interface ExtensionConfig {
  serverUrl: string;
  workspaceSlug: string;
  projectId: string;
  recentSelections: RecentSelection[];
}

export interface AnnotationRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface AnnotationPoint {
  x: number;
  y: number;
}

export interface ReviewAnnotation {
  id: string;
  kind: "pencil" | "note";
  rect?: AnnotationRect;
  pageRect?: AnnotationRect;
  strokes?: AnnotationPoint[][];
  note: string;
  targetSummary?: string;
}

export interface ReviewCapture {
  url: string;
  title: string;
  capturedAt: string;
  viewport: {
    width: number;
    height: number;
    scrollX: number;
    scrollY: number;
    devicePixelRatio: number;
  };
  annotations: ReviewAnnotation[];
  domSummary: {
    activeElement?: string;
    selectedText?: string;
    visibleText: string[];
  };
}

export interface SubmitReviewResult {
  issueId: string;
  issueIdentifier?: string;
  issueUrl: string;
}
