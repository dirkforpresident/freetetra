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

export interface HexCell {
  h: string; // H3 cell id
  lat: number;
  lon: number;
  n: number; // total samples
  u: number; // unique ISSIs
  t?: number; // unix seconds of most recent sample
  rp?: string[]; // contributing repeaters
}

export interface MapDataResponse {
  hexes: HexCell[];
  resolution: number;
  stats: {
    total_samples: number;
    unique_issis: number;
    devices_24h: number;
    repeaters_online: number;
    repeaters_total: number;
  };
}

export interface Station {
  station_id: string;
  callsign: string;
  type: "hotspot" | "repeater" | "bluestation";
  lat: number;
  lon: number;
  dl_freq: number;
  ul_freq: number;
  power_w: number;
  antenna: string;
  notes: string;
  website: string;
  last_seen: number;
  first_seen: number;
  online: boolean;
}

export interface StationsResponse {
  stations: Station[];
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
  map: (res: number, days: number) => getJSON<MapDataResponse>(`/api/map?res=${res}&days=${days}`),
  stations: () => getJSON<StationsResponse>("/api/stations"),
  positions: () => getJSON<unknown>("/api/positions"),
  peers: () => getJSON<unknown>("/api/peers"),
  telemetryClients: () => getJSON<unknown>("/api/telemetry/clients"),
  dashboardSnapshot: (sinceSeq = 0) =>
    getJSON<unknown>(`/api/dashboard/snapshot?since_seq=${sinceSeq}`),
  radioIDLookup: (issi: number) => getJSON<unknown>(`/api/radioid/lookup?issi=${issi}`),
};
