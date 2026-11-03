"use client";

import { useCallback, useEffect, useState } from "react";
import { api, DomainEntry, fmtDateTime, ListsResponse, nf } from "../../lib/api";

type Msg = { kind: "ok" | "error"; text: string } | null;

export default function Filters() {
  const [msg, setMsg] = useState<Msg>(null);
  const fail = (e: unknown) => setMsg({ kind: "error", text: e instanceof Error ? e.message : "Erreur" });

  return (
    <>
      <h1>Filtres</h1>
      {msg && (
        <div className={`notice ${msg.kind}`} role={msg.kind === "error" ? "alert" : "status"}>
          {msg.text}
        </div>
      )}
      <FilterLists onError={fail} onInfo={(t) => setMsg({ kind: "ok", text: t })} />
      <div className="grid two">
        <DomainList
          kind="allowlist"
          title="Allowlist : jamais bloqués"
          help="Ces domaines (et leurs sous-domaines) ne sont jamais bloqués, même s'ils sont dans une liste."
          onError={fail}
        />
        <DomainList
          kind="blocklist"
          title="Blocklist : toujours bloqués"
          help="Ces domaines (et leurs sous-domaines) sont toujours bloqués, en plus des listes."
          onError={fail}
        />
      </div>
    </>
  );
}

function FilterLists({ onError, onInfo }: { onError: (e: unknown) => void; onInfo: (t: string) => void }) {
  const [data, setData] = useState<ListsResponse | null>(null);
  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      setData(await api<ListsResponse>("/api/lists"));
    } catch (e) {
      onError(e);
    }
  }, [onError]);

  useEffect(() => {
    load();
  }, [load]);

  // Le téléchargement est asynchrone : on recharge le statut quelques fois.
  function refreshSoon() {
    [1500, 5000, 12000].forEach((ms) => setTimeout(load, ms));
  }

  async function add(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      setData(await api<ListsResponse>("/api/lists", { method: "POST", body: { name, url } }));
      setName("");
      setUrl("");
      onInfo("Liste ajoutée, téléchargement en cours…");
      refreshSoon();
    } catch (err) {
      onError(err);
    } finally {
      setBusy(false);
    }
  }

  async function remove(l: { url: string; name: string }) {
    if (!confirm(`Retirer la liste « ${l.name} » ?`)) return;
    try {
      setData(await api<ListsResponse>(`/api/lists?url=${encodeURIComponent(l.url)}`, { method: "DELETE" }));
      refreshSoon();
    } catch (err) {
      onError(err);
    }
  }

  async function updateNow() {
    setBusy(true);
    try {
      await api("/api/lists/update", { method: "POST" });
      onInfo("Mise à jour lancée…");
      refreshSoon();
    } catch (err) {
      onError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="card">
      <div className="row">
        <h2 style={{ margin: 0 }}>Listes de filtres</h2>
        <span className="spacer" />
        {data && <span className="muted small">{nf.format(data.totalBlocked)} domaines au total</span>}
        <button disabled={busy} onClick={updateNow}>Mettre à jour maintenant</button>
      </div>

      <div className="table-wrap" style={{ margin: "12px 0" }}>
        <table>
          <thead>
            <tr>
              <th>Liste</th>
              <th className="num">Domaines</th>
              <th>Mise à jour</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {data?.lists.map((l) => (
              <tr key={l.url}>
                <td className="name">
                  <strong>{l.name}</strong> {l.source === "config" && <span className="muted small">(config.yaml)</span>}
                  <div className="muted small mono">{l.url}</div>
                  {l.error && <div className="small" style={{ color: "var(--blocked)" }}>⚠ {l.error}</div>}
                </td>
                <td className="num">{nf.format(l.blocked)}</td>
                <td className="muted small">{l.updatedAt ? fmtDateTime(l.updatedAt) : "pas encore téléchargée"}</td>
                <td>
                  {l.source === "ui" && (
                    <button className="small danger" onClick={() => remove(l)}>Retirer</button>
                  )}
                </td>
              </tr>
            ))}
            {data && data.lists.length === 0 && (
              <tr><td colSpan={4} className="muted">Aucune liste. Ajoute-en une ci-dessous.</td></tr>
            )}
          </tbody>
        </table>
      </div>

      <form className="row grow" onSubmit={add}>
        <input type="text" placeholder="Nom (facultatif)" aria-label="Nom de la liste" value={name} onChange={(e) => setName(e.target.value)} style={{ maxWidth: 200 }} />
        <input type="text" placeholder="https://…/hosts.txt" aria-label="Adresse de la liste" value={url} onChange={(e) => setUrl(e.target.value)} required />
        <button className="primary" disabled={busy || !url}>Ajouter</button>
      </form>
      <p className="muted small" style={{ marginBottom: 0 }}>
        Formats acceptés : hosts (<code>0.0.0.0 domaine</code>), adblock (<code>||domaine^</code>) ou un domaine par ligne.
      </p>
    </section>
  );
}

function DomainList({
  kind,
  title,
  help,
  onError,
}: {
  kind: "allowlist" | "blocklist";
  title: string;
  help: string;
  onError: (e: unknown) => void;
}) {
  const [entries, setEntries] = useState<DomainEntry[]>([]);
  const [domain, setDomain] = useState("");
  const [busy, setBusy] = useState(false);
  const path = `/api/${kind}`;

  useEffect(() => {
    api<{ entries: DomainEntry[] }>(path)
      .then((r) => setEntries(r.entries))
      .catch(onError);
  }, [path, onError]);

  async function add(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      setEntries((await api<{ entries: DomainEntry[] }>(path, { method: "POST", body: { domain } })).entries);
      setDomain("");
    } catch (err) {
      onError(err);
    } finally {
      setBusy(false);
    }
  }

  async function remove(d: string) {
    try {
      setEntries((await api<{ entries: DomainEntry[] }>(`${path}?domain=${encodeURIComponent(d)}`, { method: "DELETE" })).entries);
    } catch (err) {
      onError(err);
    }
  }

  return (
    <section className="card" style={{ margin: 0 }}>
      <h2>{title}</h2>
      <p className="muted small" style={{ marginTop: -6 }}>{help}</p>
      <form className="row grow" onSubmit={add}>
        <input type="text" placeholder="exemple.com" aria-label="Domaine à ajouter" value={domain} onChange={(e) => setDomain(e.target.value)} autoCapitalize="off" spellCheck={false} />
        <button className="primary" disabled={busy || !domain.trim()}>Ajouter</button>
      </form>
      <div className="table-wrap" style={{ marginTop: 8 }}>
        <table>
          <tbody>
            {entries.map((e) => (
              <tr key={e.domain}>
                <td className="name mono">{e.domain}</td>
                <td style={{ textAlign: "right" }}>
                  {e.source === "config" ? (
                    <span className="muted small">config.yaml</span>
                  ) : (
                    <button className="small danger" onClick={() => remove(e.domain)} aria-label={`Retirer ${e.domain}`}>Retirer</button>
                  )}
                </td>
              </tr>
            ))}
            {entries.length === 0 && <tr><td className="muted">Aucun domaine.</td></tr>}
          </tbody>
        </table>
      </div>
    </section>
  );
}
