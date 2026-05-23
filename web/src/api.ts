// Typed thin wrappers around the Go backend's existing JSON endpoints.
// Paths are root-relative; Vite's dev proxy or the Go static handler routes them.

export interface SiteConfig {
  server_name: string;
  operator: string;
  host: string;
  server_info_html: string;
  lang: "de" | "en";
}

export interface LastHeardEntry {
  call_id: string;
  source_issi: number;
  callsign?: string;
  name?: string;
  dest_gssi: number;
  started_at: string; // RFC3339
  ended_at?: string;
  duration_ms: number;
  origin?: "subscriber" | "injected" | "peer";
}

export interface LastHeardResponse {
  entries: LastHeardEntry[];
}

async function getJSON<T>(path: string): Promise<T> {
  const r = await fetch(path, { credentials: "same-origin", cache: "no-store" });
  if (!r.ok) throw new Error(path + " -> " + r.status);
  return (await r.json()) as T;
}

export const api = {
  siteConfig: () => getJSON<SiteConfig>("/api/site/config"),
  publicStatus: () => getJSON<unknown>("/api/public/status"),
  liveLastHeard: () => getJSON<LastHeardResponse>("/api/live/last-heard"),
  map: (res: number, days: number) => getJSON<unknown>(`/api/map?res=${res}&days=${days}`),
  stations: () => getJSON<unknown>("/api/stations"),
  positions: () => getJSON<unknown>("/api/positions"),
  peers: () => getJSON<unknown>("/api/peers"),
  telemetryClients: () => getJSON<unknown>("/api/telemetry/clients"),
  dashboardSnapshot: (sinceSeq = 0) =>
    getJSON<unknown>(`/api/dashboard/snapshot?since_seq=${sinceSeq}`),
  radioIDLookup: (issi: number) => getJSON<unknown>(`/api/radioid/lookup?issi=${issi}`),
};
