"use client";

import { useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { api, Blocking } from "@/lib/api";

// Interrupteur du blocage : actif, désactivé, ou en pause pour N minutes.
export default function BlockingControl({
  blocking,
  onChange,
}: {
  blocking: Blocking;
  onChange: (b: Blocking) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function send(action: "on" | "off" | "pause", minutes?: number) {
    setBusy(true);
    setError("");
    try {
      onChange(await api<Blocking>("/api/blocking", { method: "POST", body: { action, minutes } }));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Erreur");
    } finally {
      setBusy(false);
    }
  }

  const until =
    blocking.mode === "paused" && blocking.pausedUntil
      ? new Date(blocking.pausedUntil).toLocaleTimeString("fr-FR", { hour: "2-digit", minute: "2-digit" })
      : "";

  return (
    <section aria-label="Contrôle du blocage" className="flex flex-wrap items-center gap-x-3 gap-y-2 border-y py-2.5">
      <span className="text-muted-foreground">Blocage</span>
      {blocking.mode === "on" && <Badge variant="ok">actif</Badge>}
      {blocking.mode === "off" && <Badge variant="destructive">désactivé</Badge>}
      {blocking.mode === "paused" && <Badge variant="warn">en pause jusqu&apos;à {until}</Badge>}
      <span className="flex-1" />
      {error && <span className="text-sm text-destructive">{error}</span>}
      {blocking.mode === "on" ? (
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="mr-1 text-xs text-muted-foreground">Pause</span>
          {[5, 30, 60].map((m) => (
            <Button key={m} variant="outline" size="sm" disabled={busy} onClick={() => send("pause", m)}>
              {m === 60 ? "1 h" : `${m} min`}
            </Button>
          ))}
          <Button variant="destructive" size="sm" disabled={busy} onClick={() => send("off")}>
            Désactiver
          </Button>
        </div>
      ) : (
        <Button size="sm" disabled={busy} onClick={() => send("on")}>
          Réactiver
        </Button>
      )}
    </section>
  );
}
