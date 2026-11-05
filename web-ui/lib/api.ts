// Client de l'API d'efBlock : tous les appels passent par ici.

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

// Déclenché quand le serveur répond 401 : AuthGate affiche alors la page de connexion.
export const UNAUTHORIZED_EVENT = "efblock:unauthorized";

export async function api<T>(
  path: string,
  opts: { method?: string; body?: unknown } = {},
): Promise<T> {
  const res = await fetch(path, {
    method: opts.method ?? "GET",
    headers: {
      "Content-Type": "application/json",
      // En-tête exigé par le serveur pour toute modification (protection CSRF).
      "X-Requested-With": "efblock",
    },
    body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
    credentials: "same-origin",
    cache: "no-store",
  });
  if (res.status === 401 && path !== "/api/login" && path !== "/api/session") {
    window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
  }
  if (!res.ok) {
    let msg = `Erreur ${res.status}`;
    try {
      msg = (await res.json()).error ?? msg;
    } catch {
      /* corps vide ou non JSON */
    }
    throw new ApiError(res.status, msg);
  }
  return res.json() as Promise<T>;
}

// --- Types des réponses ---

export type Rank = { name: string; count: number; label?: string };
export type Bucket = { start: string; total: number; blocked: number };
export type Blocking = { mode: "on" | "off" | "paused"; pausedUntil?: string };
export type UpstreamStatus = { name: string; healthy: boolean; failures: number; downUntil?: string };

export type Stats = {
  logEnabled: boolean;
  hours: number;
  summary: {
    total: number;
    blocked: number;
    cached: number;
    forwarded: number;
    local: number;
    errors: number;
    blockedPct: number;
  };
  topBlocked: Rank[];
  topDomains: Rank[];
  topClients: Rank[];
  timeline: Bucket[];
  blocking: Blocking;
  upstreams: UpstreamStatus[];
  filter: { blocked: number; exceptions: number };
  cache: { hits: number; misses: number; entries: number; stale: number } | null;
};

export type QueryEntry = {
  time: string;
  client: string;
  clientName?: string;
  name: string;
  qtype: string;
  action: "blocked" | "cached" | "forwarded" | "local" | "error";
  rule: string;
  rcode: string;
  durationUs: number;
};

export type DomainEntry = { domain: string; source: "config" | "ui" };

export type ListInfo = {
  name: string;
  url: string;
  source: "config" | "ui";
  blocked: number;
  exceptions: number;
  updatedAt?: string;
  error?: string;
};
export type ListsResponse = {
  lists: ListInfo[];
  totalBlocked: number;
  totalExceptions: number;
};

// --- Réglages ---

export type Groups = {
  upstream: { upstreams: string[]; bootstrap: string[]; timeout: string };
  access: { dns_clients: string[]; web_cidrs: string[] };
  local: { upstream: string; domains: string[]; hosts: string[] };
  filter: { block_mode: string; update_interval: string; cname_check?: boolean };
  cache: { enabled: boolean; max_entries: number; max_ttl: string; serve_stale?: boolean; stale_max?: string; prefetch?: boolean };
  querylog: { retention_days: number };
};
export type GroupName = keyof Groups;

export type SettingsView = {
  effective: Groups;
  base: Groups;
  overridden: GroupName[];
  infra: { dns_listen: string; web_listen: string; https: boolean; data_dir: string; querylog_enabled: boolean };
  canRestart: boolean;
};

// --- Groupes de clients ---

export type Group = {
  name: string;
  clients: string[];
  mode: "custom" | "off";
  all_lists: boolean;
  lists: string[];
  allow: string[];
  block: string[];
};
export type GroupsResponse = {
  groups: Group[];
  lists: { name: string; url: string }[];
  clients: { ip: string; label?: string; count: number }[];
};

export type UpstreamTestResult = { name: string; ok: boolean; latencyMs: number; error?: string };

export const nf = new Intl.NumberFormat("fr-FR");
export const fmtTime = (iso: string) =>
  new Date(iso).toLocaleTimeString("fr-FR", { hour: "2-digit", minute: "2-digit", second: "2-digit" });
export const fmtDateTime = (iso: string) =>
  new Date(iso).toLocaleString("fr-FR", { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" });
