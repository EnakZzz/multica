"use client";

import { PipelinesPage } from "@multica/views/pipelines";
import { ErrorBoundary } from "@multica/ui/components/common/error-boundary";

export default function Page() {
  return (
    <ErrorBoundary>
      <PipelinesPage />
    </ErrorBoundary>
  );
}
