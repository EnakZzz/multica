import type { Metadata } from "next";

export const metadata: Metadata = {
  title: {
    absolute: "Multica - AI Gateway Usage Report",
  },
};

export default function AIGatewayReportLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return children;
}
