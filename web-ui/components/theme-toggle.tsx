"use client";

import { Moon, Sun } from "lucide-react";
import { useEffect, useState } from "react";

import { Button } from "@/components/ui/button";

// Bascule sombre / clair. Le choix est gardé dans ce navigateur seulement (rien côté serveur).
export default function ThemeToggle() {
  const [light, setLight] = useState(false);
  useEffect(() => setLight(document.documentElement.dataset.theme === "light"), []);

  function toggle() {
    const next = !light;
    setLight(next);
    if (next) document.documentElement.dataset.theme = "light";
    else delete document.documentElement.dataset.theme;
    try {
      localStorage.setItem("efblock-theme", next ? "light" : "dark");
    } catch {
      /* navigation privée : le choix ne sera simplement pas retenu */
    }
  }

  return (
    <Button variant="ghost" size="sm" onClick={toggle} className="justify-start gap-2 text-muted-foreground" title="Changer de thème">
      {light ? <Moon className="size-4" aria-hidden /> : <Sun className="size-4" aria-hidden />}
      <span className="hidden md:inline">{light ? "Thème sombre" : "Thème clair"}</span>
      <span className="sr-only md:hidden">{light ? "Thème sombre" : "Thème clair"}</span>
    </Button>
  );
}
