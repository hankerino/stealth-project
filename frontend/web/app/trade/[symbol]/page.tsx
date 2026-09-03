import { Shell } from "@/components/shell";
import { TradeView } from "@/components/trade-view";

export default async function TradePage({ params }: { params: Promise<{ symbol: string }> }) {
  const { symbol } = await params;
  return (
    <Shell>
      <TradeView symbol={decodeURIComponent(symbol)} />
    </Shell>
  );
}
