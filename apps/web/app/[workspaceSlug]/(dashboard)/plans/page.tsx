"use client";

import { PlansPage } from "@multica/views/plans";
import { ErrorBoundary } from "@multica/ui/components/common/error-boundary";

export default function Page() {
  return (
    <ErrorBoundary>
      <PlansPage />
    </ErrorBoundary>
  );
}
