"use client";

import { useCallback, useEffect, useState } from "react";

import ActivityChart from "@/components/activity-chart";
import BlockingControl from "@/components/blocking-control";
import TopTable from "@/components/top-table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, Blocking, nf, Stats } from "@/lib/api";

const pct = (n: number) => n.toFixed(1).replace(".", ",") + " %";

export default function Dashboard() {
  const [hours, setHours] = useState("24");
  const [stats, setStats] = useState<Stats | null>(null);
  const [error, setError] = useState("");
  const [allowed, setAllowed] = useState<string[]>([]);

  const load = useCallback(async () => {
    try {
      setStats(await api<Stats>(`/api/stats?hours=${hours}`));
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "Erreur");
    }
  }, [hours]);

  // Chargement au démarrage, au changement de période, puis à intervalle régulier :
  // plus espacé sur les longues périodes (le calcul est plus coûteux), et suspendu
  // quand l'onglet n'est pas visible.
  useEffect(() => {
    load();
    const every = { "1": 5_000, "24": 10_000, "168": 30_000 }[hours] ?? 10_000;
    const id = setInterval(() => {
      if (!document.hidden) load();
    }, every);
    const onVisible = () => {
      if (!document.hidden) load();
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      clearInterval(id);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [load, hours]);

  async function allow(domain: string) {
    try {
      await api("/api/allowlist", { method: "POST", body: { domain } });
      setAllowed((a) => [...a, domain]);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Erreur");
    }
  }

  if (!stats) return <p className="text-muted-foreground">{error || "Chargement…"}</p>;

  const s = stats.summary;
  const cachePct = s.total ? (s.cached * 100) / s.total : 0;

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-lg font-semibold tracking-tight">Tableau de bord</h1>
        <Tabs value={hours} onValueChange={setHours}>
          <TabsList aria-label="Période">
            <TabsTrigger value="1">1 h</TabsTrigger>
            <TabsTrigger value="24">24 h</TabsTrigger>
            <TabsTrigger value="168">7 j</TabsTrigger>
          </TabsList>
        </Tabs>
      </div>

      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}

      <BlockingControl blocking={stats.blocking} onChange={(b: Blocking) => setStats({ ...stats, blocking: b })} />
      <UpstreamStrip upstreams={stats.upstreams} />

      {!stats.logEnabled ? (
        <p className="text-muted-foreground">
          Le journal des requêtes est désactivé (<code>querylog.enabled: false</code>) : pas de statistiques.
        </p>
      ) : (
        <>
          <dl className="grid grid-cols-2 divide-x divide-y border md:grid-cols-4 md:divide-y-0">
            <Figure label="Requêtes" value={nf.format(s.total)} sub={`${nf.format(s.forwarded)} transmises, ${nf.format(s.local)} locales`} />
            <Figure
              label="Bloquées"
              value={nf.format(s.blocked)}
              sub={`${pct(s.blockedPct)} du total`}
              valueClass="text-blocked"
            />
            <Figure
              label="Cache"
              value={nf.format(s.cached)}
              sub={`${pct(cachePct)} du total, ${nf.format(stats.cache?.entries ?? 0)} entrées`}
            />
            <Figure
              label="Domaines dans les listes"
              value={nf.format(stats.filter.blocked)}
              sub={s.errors > 0 ? `${nf.format(s.errors)} échec(s) upstream` : "aucun échec upstream"}
            />
          </dl>

          <section>
            <h2 className="mb-3 text-sm font-semibold">Requêtes par tranche de temps</h2>
            <ActivityChart data={stats.timeline} hours={stats.hours} />
          </section>

          <div className="grid gap-8 lg:grid-cols-2">
            <TopTable
              title="Domaines les plus bloqués"
              rows={stats.topBlocked}
              empty="Aucun domaine bloqué sur cette période."
              action={(name) =>
                allowed.includes(name) ? (
                  <span className="text-xs text-muted-foreground">autorisé</span>
                ) : (
                  <Button variant="outline" size="sm" onClick={() => allow(name)} title="Ne plus bloquer ce domaine">
                    Autoriser
                  </Button>
                )
              }
            />
            <TopTable title="Clients les plus actifs" rows={stats.topClients} />
          </div>
          <TopTable title="Domaines les plus demandés" rows={stats.topDomains} />
        </>
      )}
    </div>
  );
}

// État des résolveurs amont : un upstream en panne est mis de côté un moment, on l'affiche.
function UpstreamStrip({ upstreams }: { upstreams: Stats["upstreams"] }) {
  if (upstreams.length === 0) return null;
  const time = (iso?: string) => (iso ? new Date(iso).toLocaleTimeString("fr-FR", { hour: "2-digit", minute: "2-digit", second: "2-digit" }) : "");
  return (
    <section aria-label="Résolveurs amont" className="-mt-3 flex flex-wrap items-center gap-x-4 gap-y-1.5 text-xs">
      <span className="text-muted-foreground">Upstreams</span>
      {upstreams.map((u) => (
        <span key={u.name} className="flex items-center gap-1.5">
          <span className="font-mono">{u.name}</span>
          {u.healthy ? (
            <Badge variant="ok">ok</Badge>
          ) : (
            <Badge variant="warn">en panne, nouvel essai à {time(u.downUntil)}</Badge>
          )}
        </span>
      ))}
    </section>
  );
}

function Figure({ label, value, sub, valueClass = "" }: { label: string; value: string; sub: string; valueClass?: string }) {
  return (
    <div className="px-4 py-3">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className={`mt-0.5 text-2xl font-semibold tabular-nums ${valueClass}`}>{value}</dd>
      <dd className="mt-0.5 text-xs text-muted-foreground">{sub}</dd>
    </div>
  );
}
