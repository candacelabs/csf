import createClient from "openapi-fetch";
import type { components, paths } from "./csf-schema";

export function csfURL(path: string) {
  return new URL(path, new URL(import.meta.env.VITE_CSF_DASHBOARD_URL || "/", window.location.href)).href;
}

export const csf = createClient<paths>({
  baseUrl: csfURL("/"),
  fetch: (request: Request) => globalThis.fetch(request),
});
export type SimulationRun = components["schemas"]["candace.brainspine.v1.SimulationRun"];
