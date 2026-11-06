"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import ConfirmButton from "@/components/confirm-button";
import { Button } from "@/components/ui/button";
import { api, fmtDateTime, UpdateInfo } from "@/lib/api";

// Une mise à jour dure quelques dizaines de secondes (téléchargement, installation, redémarrage
// du service). On interroge le serveur toutes les 2 s ; pendant le redémarrage il ne répond pas,
// ce qui est normal : on continue d'essayer.
const POLL_MS = 2000;
const GIVE_UP_MS = 10 * 60_000;
// Un résultat déjà présent juste après la demande est celui de la mise à jour PRÉCÉDENTE :
// tant qu'on n'a pas vu « en cours », on ne le croit qu'après ce délai.
const STALE_GUARD_MS = 20_000;

export default function UpdateSection() {
  const [info, setInfo] = useState<UpdateInfo | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [running, setRunning] = useState(false);
  const [progress, setProgress] = useState("");
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const load = useCallback(async (refresh: boolean) => {
    setBusy(true);
    setError("");
    try {
      setInfo(await api<UpdateInfo>(`/api/update${refresh ? "?refresh=1" : ""}`));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Erreur");
    } finally {
      setBusy(false);
    }
  }, []);

  useEffect(() => {
    void load(false);
    return () => {
      if (timer.current) clearTimeout(timer.current);
    };
  }, [load]);

  const apply = useCallback(async () => {
    setError("");
    try {
      await api("/api/update", { method: "POST" });
    } catch (e) {
      setError(e instanceof Error ? e.message : "Erreur");
      return;
    }
    setRunning(true);
    setProgress("Demande envoyée…");
    const started = Date.now();
    let sawRunning = false;

    const poll = async () => {
      if (Date.now() - started > GIVE_UP_MS) {
        setRunning(false);
        setError("La mise à jour n'a pas répondu après 10 minutes. Vérifie la console de la machine.");
        return;
      }
      try {
        const i = await api<UpdateInfo>("/api/update");
        const st = i.status;
        if (st.state === "running") {
          sawRunning = true;
          setProgress(st.message || "Mise à jour en cours…");
        } else if ((st.state === "ok" || st.state === "failed") && (sawRunning || Date.now() - started > STALE_GUARD_MS)) {
          if (st.state === "ok") {
            setProgress("Mise à jour terminée, rechargement de la page…");
            setTimeout(() => location.reload(), 1500);
            return;
          }
          setRunning(false);
          setInfo(i);
          setError(`La mise à jour a échoué : ${st.message || "erreur inconnue"}`);
          return;
        }
      } catch {
        // Le service redémarre : sans réponse, on réessaie.
        if (sawRunning) setProgress("Redémarrage d'efBlock…");
      }
      timer.current = setTimeout(poll, POLL_MS);
    };
    timer.current = setTimeout(poll, POLL_MS);
  }, []);

  return (
    <section className="max-w-2xl border-t pt-5">
      <h2 className="mb-1 text-sm font-semibold">Mises à jour</h2>
      <p className="mb-3 text-muted-foreground">
        efBlock vérifie la dernière version publiée sur GitHub. Ta configuration, ton mot de passe, tes listes et ton journal sont conservés.
        L&apos;installation vérifie l&apos;empreinte du paquet téléchargé.
      </p>

      {info && (
        <dl className="mb-4 grid grid-cols-[auto_1fr] gap-x-6 gap-y-1.5">
          <dt className="text-muted-foreground">Version installée</dt>
          <dd className="font-mono text-xs">{info.current}</dd>
          <dt className="text-muted-foreground">Dernière version</dt>
          <dd className="font-mono text-xs">{info.latest || "inconnue"}</dd>
          {info.checked && (
            <>
              <dt className="text-muted-foreground">Dernière vérification</dt>
              <dd className="text-xs">{fmtDateTime(info.checked)}</dd>
            </>
          )}
        </dl>
      )}

      {info?.error && (
        <p role="alert" className="mb-3 text-sm text-destructive">
          {info.error}
        </p>
      )}

      {running ? (
        <p role="status" className="text-muted-foreground">
          {progress} <span className="text-xs">Ne ferme pas la page : elle se rechargera toute seule.</span>
        </p>
      ) : (
        <div className="flex flex-wrap items-center gap-3">
          {info?.newer && info.can_apply && <ConfirmButton variant="default" label={`Mettre à jour vers ${info.latest}`} onConfirm={apply} />}
          <Button variant="outline" size="sm" disabled={busy} onClick={() => load(true)}>
            Vérifier maintenant
          </Button>
          {info && !info.newer && !info.error && info.latest && <span className="text-sm text-muted-foreground">efBlock est à jour.</span>}
        </div>
      )}

      {info?.newer && !info.can_apply && info.reason && <p className="mt-3 text-xs text-muted-foreground">{info.reason}.</p>}
      {error && (
        <p role="alert" className="mt-3 text-sm text-destructive">
          {error}
        </p>
      )}
    </section>
  );
}
