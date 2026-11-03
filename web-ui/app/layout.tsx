import type { Metadata } from "next";

import "./globals.css";
import AuthGate from "@/components/auth-gate";

export const metadata: Metadata = {
  title: "efBlock",
  description: "Bloqueur de publicités DNS",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="fr">
      <body>
        <AuthGate>{children}</AuthGate>
      </body>
    </html>
  );
}
