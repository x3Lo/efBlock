"use client";

import { Activity, Ban, BarChart3, Globe, ListFilter, MonitorSmartphone, Server, Zap } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import ActivityChart from "@/components/activity-chart";
import BlockingControl from "@/components/blocking-control";
import TopTable from "@/components/top-table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
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
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold tracking-tight">Tableau de bord</h1>
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

      <BlockingControl
        blocking={stats.blocking}
        onChange={(b: Blocking) => setStats({ ...stats, blocking: b })}
        blocked={s.blocked}
        blockedPct={s.blockedPct}
        hours={stats.hours}
        logEnabled={stats.logEnabled}
      />

      {!stats.logEnabled ? (
        <Card className="p-4 text-muted-foreground">
          Le journal des requêtes est désactivé (<code>querylog.enabled: false</code>) : pas de statistiques.
        </Card>
      ) : (
        <>
          <dl className="grid grid-cols-2 gap-3 lg:grid-cols-4">
            <Figure icon={Activity} label="Requêtes" value={nf.format(s.total)} sub={`${nf.format(s.forwarded)} transmises, ${nf.format(s.local)} locales`} />
            <Figure icon={Ban} label="Bloquées" value={nf.format(s.blocked)} sub={`${pct(s.blockedPct)} du total`} tone="text-blocked" />
            <Figure
              icon={Zap}
              label="Servies par le cache"
              value={nf.format(s.cached)}
              sub={`${pct(cachePct)} du total, ${nf.format(stats.cache?.entries ?? 0)} entrées${stats.cache?.stale ? `, ${nf.format(stats.cache.stale)} périmées servies` : ""}`}
            />
            <Figure
              icon={ListFilter}
              label="Domaines dans les listes"
              value={nf.format(stats.filter.blocked)}
              sub={stats.filter.blocked === 0 ? "aucune liste : ajoute-en dans Filtres" : s.errors > 0 ? `${nf.format(s.errors)} échec(s) upstream` : "aucun échec upstream"}
              tone={stats.filter.blocked === 0 ? "text-warn" : ""}
            />
          </dl>

          <Card>
            <CardHeader title="Requêtes par tranche de temps" icon={BarChart3} />
            <div className="px-3 pb-4">
              <ActivityChart data={stats.timeline} hours={stats.hours} />
            </div>
          </Card>

          <div className="grid gap-4 lg:grid-cols-2">
            <TopTable
              icon={Ban}
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
            <TopTable icon={MonitorSmartphone} title="Clients les plus actifs" rows={stats.topClients} />
          </div>
          <div className="grid gap-4 lg:grid-cols-2">
            <TopTable icon={Globe} title="Domaines les plus demandés" rows={stats.topDomains} />
            <UpstreamCard upstreams={stats.upstreams} />
          </div>
        </>
      )}
    </div>
  );
}

// État des résolveurs amont : un upstream en panne est mis de côté un moment, on l'affiche.
function UpstreamCard({ upstreams }: { upstreams: Stats["upstreams"] }) {
  const time = (iso?: string) => (iso ? new Date(iso).toLocaleTimeString("fr-FR", { hour: "2-digit", minute: "2-digit", second: "2-digit" }) : "");
  return (
    <Card>
      <CardHeader title="Résolveurs amont" icon={Server} />
      <ul className="flex flex-col gap-2 px-4 pb-4">
        {upstreams.length === 0 && <li className="text-muted-foreground">Aucun résolveur configuré.</li>}
        {upstreams.map((u) => (
          <li key={u.name} className="flex flex-wrap items-center justify-between gap-2">
            <span className="font-mono text-xs break-all">{u.name}</span>
            {u.healthy ? <Badge variant="ok">en marche</Badge> : <Badge variant="warn">en panne, nouvel essai à {time(u.downUntil)}</Badge>}
          </li>
        ))}
      </ul>
    </Card>
  );
}

function Figure({
  icon: Icon,
  label,
  value,
  sub,
  tone = "",
}: {
  icon: React.ComponentType<{ className?: string }>;
  label: string;
  value: string;
  sub: string;
  tone?: string;
}) {
  return (
    <Card className="px-4 py-3.5">
      <dt className="flex items-center gap-2 text-xs text-muted-foreground">
        <Icon className="size-3.5" aria-hidden />
        {label}
      </dt>
      <dd className={`mt-1 text-2xl font-semibold tabular-nums ${tone}`}>{value}</dd>
      <dd className="mt-0.5 text-xs text-muted-foreground">{sub}</dd>
    </Card>
  );
}
