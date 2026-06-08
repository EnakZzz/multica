"use client";

import { ErrorBoundary } from "@multica/ui/components/common/error-boundary";
import { PipelineDetailPage } from "@multica/views/pipelines";

export default function Page() {
  return (
    <ErrorBoundary>
      <PipelineDetailPage />
    </ErrorBoundary>
  );
}
