"use client";

import { useCallback, useEffect, useState } from "react";
import BlockingControl from "../components/BlockingControl";
import Timeline from "../components/Timeline";
import TopTable from "../components/TopTable";
import { api, Blocking, nf, Stats } from "../lib/api";

export default function Dashboard() {
  const [hours, setHours] = useState(24);
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

  // Chargement au démarrage, au changement de période, puis toutes les 10 s.
  useEffect(() => {
    load();
    const id = setInterval(load, 10_000);
    return () => clearInterval(id);
  }, [load]);

  async function allow(domain: string) {
    try {
      await api("/api/allowlist", { method: "POST", body: { domain } });
      setAllowed((a) => [...a, domain]);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Erreur");
    }
  }

  if (!stats) return <p className="center">{error || "Chargement…"}</p>;

  const s = stats.summary;
  const cachePct = s.total ? (s.cached * 100) / s.total : 0;
  const period = { 1: "dernière heure", 24: "dernières 24 h", 168: "7 derniers jours" }[stats.hours] ?? "";

  return (
    <>
      <div className="row" style={{ marginBottom: 8 }}>
        <h1 style={{ margin: 0 }}>Tableau de bord</h1>
        <span className="spacer" />
        <label className="row small muted">
          Période
          <select value={hours} onChange={(e) => setHours(Number(e.target.value))}>
            <option value={1}>1 heure</option>
            <option value={24}>24 heures</option>
            <option value={168}>7 jours</option>
          </select>
        </label>
      </div>

      {error && <div className="notice error" role="alert">{error}</div>}
      <BlockingControl
        blocking={stats.blocking}
        onChange={(b: Blocking) => setStats({ ...stats, blocking: b })}
      />

      {!stats.logEnabled ? (
        <div className="notice">
          Le journal des requêtes est désactivé (<code>querylog.enabled: false</code>) : pas de statistiques.
        </div>
      ) : (
        <>
          <div className="grid stats" style={{ marginBottom: 16 }}>
            <div className="card stat" style={{ margin: 0 }}>
              <div className="label">Requêtes ({period})</div>
              <div className="value">{nf.format(s.total)}</div>
              <div className="sub">{nf.format(s.forwarded)} transmises à l&apos;upstream</div>
            </div>
            <div className="card stat" style={{ margin: 0 }}>
              <div className="label">Bloquées</div>
              <div className="value" style={{ color: "var(--blocked)" }}>{nf.format(s.blocked)}</div>
              <div className="sub">{s.blockedPct.toFixed(1).replace(".", ",")} % des requêtes</div>
            </div>
            <div className="card stat" style={{ margin: 0 }}>
              <div className="label">Servies par le cache</div>
              <div className="value">{nf.format(s.cached)}</div>
              <div className="sub">{cachePct.toFixed(1).replace(".", ",")} % · {nf.format(stats.cache?.Entries ?? 0)} en mémoire</div>
            </div>
            <div className="card stat" style={{ margin: 0 }}>
              <div className="label">Domaines dans les listes</div>
              <div className="value">{nf.format(stats.filter.Blocked)}</div>
              <div className="sub">{s.errors > 0 ? `${nf.format(s.errors)} échec(s) upstream` : "aucun échec upstream"}</div>
            </div>
          </div>

          <section className="card">
            <h2>Activité</h2>
            <Timeline data={stats.timeline} hours={stats.hours} />
          </section>

          <div className="grid two">
            <TopTable
              title="Domaines les plus bloqués"
              rows={stats.topBlocked}
              empty="Aucun domaine bloqué sur cette période."
              action={(name) =>
                allowed.includes(name) ? (
                  <span className="muted small">autorisé ✓</span>
                ) : (
                  <button className="small" onClick={() => allow(name)} title="Ne plus bloquer ce domaine">
                    Autoriser
                  </button>
                )
              }
            />
            <TopTable title="Clients les plus actifs" rows={stats.topClients} />
          </div>
          <TopTable title="Domaines les plus demandés" rows={stats.topDomains} />
        </>
      )}
    </>
  );
}
