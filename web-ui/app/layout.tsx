import type { Metadata } from "next";

import "./globals.css";
import AuthGate from "@/components/auth-gate";

export const metadata: Metadata = {
  title: "efBlock",
  description: "Bloqueur de publicités DNS",
};

// Applique le thème choisi AVANT le premier affichage (sinon la page clignoterait en sombre).
const themeScript = `try{if(localStorage.getItem("efblock-theme")==="light")document.documentElement.dataset.theme="light"}catch(e){}`;

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="fr" suppressHydrationWarning>
      <head>
        <script dangerouslySetInnerHTML={{ __html: themeScript }} />
      </head>
      <body>
        <AuthGate>{children}</AuthGate>
      </body>
    </html>
  );
}
