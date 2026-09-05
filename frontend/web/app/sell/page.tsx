import type { Metadata } from "next";
import { SellPage } from "@/components/sell";

export const metadata: Metadata = {
  title: "Sell GPU capacity — become a founding provider",
  description:
    "List idle H100 / A100 / B200 hours on hQube Exchange with a one-line install and get paid when a trade settles. Founding providers set the opening prices.",
  openGraph: {
    title: "Your GPUs are earning $0 while they sleep.",
    description: "List idle GPU-hours on a real exchange. Founding providers set the opening prices.",
    images: [{ url: "/social/launch-card.png", width: 1600, height: 900 }],
  },
};

export default function Page() {
  return <SellPage />;
}
