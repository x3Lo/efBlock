"use client";

import { useCallback, useEffect, useState } from "react";
import { api, fmtTime, QueryEntry } from "../../lib/api";

const LABEL: Record<QueryEntry["action"], string> = {
  blocked: "bloquée",
  cached: "cache",
  forwarded: "transmise",
  error: "échec",
};

export default function Queries() {
  const [search, setSearch] = useState("");
  const [debounced, setDebounced] = useState("");
  const [rows, setRows] = useState<QueryEntry[]>([]);
  const [logEnabled, setLogEnabled] = useState(true);
  const [message, setMessage] = useState<{ kind: "ok" | "error"; text: string } | null>(null);
  const [loaded, setLoaded] = useState(false);

  // On attend que la saisie se calme (300 ms) avant d'interroger le serveur.
  useEffect(() => {
    const id = setTimeout(() => setDebounced(search.trim()), 300);
    return () => clearTimeout(id);
  }, [search]);

  const load = useCallback(async () => {
    try {
      const r = await api<{ logEnabled: boolean; entries: QueryEntry[] }>(
        `/api/queries?limit=200&q=${encodeURIComponent(debounced)}`,
      );
      setRows(r.entries);
      setLogEnabled(r.logEnabled);
    } catch (e) {
      setMessage({ kind: "error", text: e instanceof Error ? e.message : "Erreur" });
    } finally {
      setLoaded(true);
    }
  }, [debounced]);

  useEffect(() => {
    load();
    const id = setInterval(load, 5_000);
    return () => clearInterval(id);
  }, [load]);

  async function act(e: QueryEntry) {
    const toAllow = e.action === "blocked";
    try {
      await api(toAllow ? "/api/allowlist" : "/api/blocklist", { method: "POST", body: { domain: e.name } });
      setMessage({ kind: "ok", text: `${e.name} ${toAllow ? "ne sera plus bloqué" : "sera désormais bloqué"}.` });
    } catch (err) {
      setMessage({ kind: "error", text: err instanceof Error ? err.message : "Erreur" });
    }
  }

  return (
    <>
      <h1>Journal des requêtes</h1>
      <div className="row grow" style={{ marginBottom: 12 }}>
        <input
          type="search"
          placeholder="Filtrer par domaine ou par client…"
          aria-label="Filtrer le journal"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </div>
      {message && (
        <div className={`notice ${message.kind}`} role="status">
          {message.text}
        </div>
      )}
      {!logEnabled ? (
        <div className="notice">Le journal des requêtes est désactivé.</div>
      ) : !loaded ? (
        <p className="center">Chargement…</p>
      ) : rows.length === 0 ? (
        <p className="center">Aucune requête{debounced ? " pour cette recherche" : " enregistrée"}.</p>
      ) : (
        <div className="card table-wrap">
          <table>
            <thead>
              <tr>
                <th>Heure</th>
                <th>Client</th>
                <th>Domaine</th>
                <th>Type</th>
                <th>Résultat</th>
                <th className="num">Durée</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {rows.map((e, i) => (
                <tr key={`${e.time}-${e.name}-${e.qtype}-${i}`}>
                  <td className="mono muted">{fmtTime(e.time)}</td>
                  <td className="mono">{e.client}</td>
                  <td className="name mono">
                    {e.name}
                    {e.rule && e.rule !== e.name && <div className="muted small">règle : {e.rule}</div>}
                  </td>
                  <td className="muted">{e.qtype}</td>
                  <td><span className={`badge ${e.action}`}>{LABEL[e.action]}</span></td>
                  <td className="num muted">
                    {e.durationUs >= 1000 ? `${(e.durationUs / 1000).toFixed(0)} ms` : `${e.durationUs} µs`}
                  </td>
                  <td>
                    {(e.action === "blocked" || e.action === "forwarded" || e.action === "cached") && (
                      <button className="small" onClick={() => act(e)}>
                        {e.action === "blocked" ? "Autoriser" : "Bloquer"}
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}
