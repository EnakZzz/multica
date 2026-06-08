"use client";

import { ErrorBoundary } from "@multica/ui/components/common/error-boundary";
import { PipelinesPage } from "@multica/views/pipelines";

export default function Page() {
  return (
    <ErrorBoundary>
      <PipelinesPage />
    </ErrorBoundary>
  );
}
