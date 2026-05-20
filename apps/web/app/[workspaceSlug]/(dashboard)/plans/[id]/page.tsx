"use client";

import { PlanDetailPage } from "@multica/views/plans";
import { ErrorBoundary } from "@multica/ui/components/common/error-boundary";

export default function Page() {
  return (
    <ErrorBoundary>
      <PlanDetailPage />
    </ErrorBoundary>
  );
}
