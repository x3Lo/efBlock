"use client";

import { useState } from "react";
import { api, Blocking } from "../lib/api";

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

  const label = { on: "Blocage actif", off: "Blocage désactivé", paused: "Blocage en pause" }[blocking.mode];
  const until =
    blocking.mode === "paused" && blocking.pausedUntil
      ? new Date(blocking.pausedUntil).toLocaleTimeString("fr-FR", { hour: "2-digit", minute: "2-digit" })
      : "";

  return (
    <section className="card" aria-label="Contrôle du blocage">
      <div className="row">
        <span className={`badge ${blocking.mode}`}>{label}</span>
        {until && <span className="muted">reprend à {until}</span>}
        <span className="spacer" />
        {blocking.mode === "on" ? (
          <>
            <span className="muted small">Mettre en pause :</span>
            {[5, 30, 60].map((m) => (
              <button key={m} disabled={busy} onClick={() => send("pause", m)}>
                {m === 60 ? "1 h" : `${m} min`}
              </button>
            ))}
            <button className="danger" disabled={busy} onClick={() => send("off")}>
              Désactiver
            </button>
          </>
        ) : (
          <button className="primary" disabled={busy} onClick={() => send("on")}>
            Réactiver maintenant
          </button>
        )}
      </div>
      {error && <div className="notice error" role="alert" style={{ marginTop: 10 }}>{error}</div>}
    </section>
  );
}
