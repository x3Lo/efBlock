"use client";

import { ShieldAlert, ShieldCheck, ShieldOff } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { api, Blocking, nf } from "@/lib/api";
import { cn } from "@/lib/utils";

const period = (hours: number) => (hours <= 1 ? "la dernière heure" : hours <= 24 ? "les dernières 24 h" : "les 7 derniers jours");

// État de la protection, en grand en tête du tableau de bord, avec ses commandes :
// actif, désactivé, ou en pause pour N minutes.
export default function BlockingControl({
  blocking,
  onChange,
  blocked,
  blockedPct,
  hours,
  logEnabled,
}: {
  blocking: Blocking;
  onChange: (b: Blocking) => void;
  blocked: number;
  blockedPct: number;
  hours: number;
  logEnabled: boolean;
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

  const look = {
    on: { Icon: ShieldCheck, title: "Protection active", tone: "text-ok", bg: "bg-ok/15", ring: "border-ok/30" },
    paused: { Icon: ShieldAlert, title: `Protection en pause jusqu'à ${until}`, tone: "text-warn", bg: "bg-warn/15", ring: "border-warn/40" },
    off: { Icon: ShieldOff, title: "Protection désactivée", tone: "text-destructive", bg: "bg-destructive/15", ring: "border-destructive/40" },
  }[blocking.mode];

  const pct = blockedPct.toFixed(1).replace(".", ",");
  const sentence =
    blocking.mode === "on"
      ? logEnabled
        ? `${nf.format(blocked)} publicités et traqueurs bloqués sur ${period(hours)} (${pct} % des requêtes).`
        : "Les publicités et traqueurs sont bloqués pour tout le réseau."
      : blocking.mode === "paused"
        ? "Rien n'est bloqué pendant la pause ; le blocage reprendra tout seul."
        : "Rien n'est bloqué. Réactive la protection pour bloquer publicités et traqueurs.";

  return (
    <Card aria-label="Contrôle du blocage" className={cn("flex flex-col gap-4 p-5 sm:flex-row sm:items-center", look.ring)}>
      <div className="flex flex-1 items-center gap-4">
        <span className={cn("flex size-12 shrink-0 items-center justify-center rounded-full", look.bg, look.tone)}>
          <look.Icon className="size-6" aria-hidden />
        </span>
        <div>
          <p className={cn("text-lg font-semibold tracking-tight", look.tone)}>{look.title}</p>
          <p className="mt-0.5 text-muted-foreground">{sentence}</p>
          {error && (
            <p role="alert" className="mt-1 text-sm text-destructive">
              {error}
            </p>
          )}
        </div>
      </div>
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
        <Button disabled={busy} onClick={() => send("on")}>
          Réactiver la protection
        </Button>
      )}
    </Card>
  );
}
