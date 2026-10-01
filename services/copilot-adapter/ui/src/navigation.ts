// Deployment supplies destinations; a standalone Workbench has no CSF links.
export function csfDestinations() {
  const dashboard = import.meta.env.VITE_CSF_DASHBOARD_URL?.trim();
  if (!dashboard) return [];
  const destinations = [
    { name: "CSF dashboard", href: dashboard, symbol: "◈", description: "Progress, workers, runtime inspection and observability links. Reports carry their observation times." },
    { name: "Simulation runs", href: "#/simulations", symbol: "◎", description: "CARLA and Isaac Sim runs, screenshots, recordings and execution evidence." },
  ];
  const release = import.meta.env.VITE_CSF_RELEASE_URL?.trim();
  if (release) destinations.push({
    name: "Release & evidence", href: "#/release", symbol: "↗",
    description: "The packaged source, architecture, verification receipts and remaining consumer setup.",
  });
  return destinations;
}
