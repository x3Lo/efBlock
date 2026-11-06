"use client";

import Link from "next/link";
import { useEffect, useState } from "react";

import { api, Connectivity, UpdateInfo } from "@/lib/api";
import { cn } from "@/lib/utils";

// Indicateurs toujours visibles dans la barre latérale : accès à internet, et mise à jour disponible.
// L'accès à internet est revérifié toutes les 30 s (le serveur garde le résultat 15 s) ;
// la mise à jour toutes les 10 min (le serveur ne contacte GitHub qu'une fois par période).

const CONNECTIVITY_MS = 30_000;
const UPDATE_MS = 10 * 60_000;

function useRepeat<T>(path: string, every: number) {
  const [data, setData] = useState<T | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let alive = true;
    const run = () => {
      if (document.visibilityState === "hidden") return; // onglet caché : inutile d'interroger
      api<T>(path)
        .then((d) => alive && (setData(d), setFailed(false)))
        .catch(() => alive && setFailed(true));
    };
    run();
    const id = setInterval(run, every);
    document.addEventListener("visibilitychange", run);
    return () => {
      alive = false;
      clearInterval(id);
      document.removeEventListener("visibilitychange", run);
    };
  }, [path, every]);
  return { data, failed };
}

function Dot({ tone }: { tone: "ok" | "bad" | "unknown" }) {
  return (
    <span
      aria-hidden
      className={cn(
        "inline-block size-2 shrink-0 rounded-full",
        tone === "ok" && "bg-emerald-500",
        tone === "bad" && "bg-red-500",
        tone === "unknown" && "bg-muted-foreground/40",
      )}
    />
  );
}

export default function StatusIndicators() {
  const conn = useRepeat<Connectivity>("/api/connectivity", CONNECTIVITY_MS);
  const upd = useRepeat<UpdateInfo>("/api/update", UPDATE_MS);

  const c = conn.data;
  const tone = conn.failed || !c ? "unknown" : c.dns ? "ok" : "bad";
  const label = conn.failed ? "Serveur injoignable" : !c ? "Internet…" : c.dns ? "Internet" : "Pas d'internet";
  const detail = c
    ? c.dns
      ? `Les résolveurs amont répondent (${c.latency_ms} ms, ${c.upstreams_healthy}/${c.upstreams_total} en marche).`
      : (c.error ?? "Aucun résolveur amont ne répond.")
    : "Vérification en cours.";

  const u = upd.data;
  const showUpdate = !!u && u.newer && u.status.state !== "running";

  return (
    <div className="flex flex-col gap-1.5 text-xs md:px-2">
      <div role="status" title={detail} className="flex items-center gap-2 text-muted-foreground">
        <Dot tone={tone} />
        <span>
          {label}
          {c?.dns && <span className="ml-1 tabular-nums opacity-70">{c.latency_ms} ms</span>}
        </span>
      </div>
      {showUpdate && (
        <Link
          href="/settings/"
          className="flex items-center gap-2 rounded-md bg-primary px-2 py-1 font-medium text-primary-foreground hover:bg-primary/85"
        >
          Mise à jour {u.latest} disponible
        </Link>
      )}
    </div>
  );
}
