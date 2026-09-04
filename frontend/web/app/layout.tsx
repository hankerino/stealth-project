import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  metadataBase: new URL("https://exchange.hqube.co"),
  title: { default: "hQube Exchange", template: "%s · hQube Exchange" },
  description: "Buy, sell and resell GPU compute hours — spot and forward markets, escrow-backed, on verified hardware. By hQube.",
  openGraph: { title: "hQube Exchange", description: "The exchange for GPU compute hours.", siteName: "hQube Exchange", type: "website" },
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <head>
        {/* Brand font (same as hqube.co). Loaded at runtime so builds never depend on Google's CDN. */}
        <link rel="preconnect" href="https://fonts.googleapis.com" />
        <link rel="preconnect" href="https://fonts.gstatic.com" crossOrigin="anonymous" />
        <link href="https://fonts.googleapis.com/css2?family=Outfit:wght@400;500;600;700&display=swap" rel="stylesheet" />
      </head>
      <body className="min-h-screen font-sans">{children}</body>
    </html>
  );
}
