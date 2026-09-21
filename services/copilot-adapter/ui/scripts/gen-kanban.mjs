// Vite already owns TSX compilation; React/Mantine own every emitted class/style.
import { createServer } from "vite";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
const directory = new URL("../../kanban/templates/", import.meta.url);
const server = await createServer({ server: { middlewareMode: true, hmr: false, watch: null }, appType: "custom", optimizeDeps: { noDiscovery: true, include: [] } });
try {
  const { renderKanbanTemplates } = await server.ssrLoadModule("/src/kanbanTemplates.tsx");
  await mkdir(directory, { recursive: true });
  for (const [name, html] of Object.entries(renderKanbanTemplates())) {
    const path = new URL(name, directory);
    if (process.argv.includes("--write")) await writeFile(path, html);
    else if (await readFile(path, "utf8") !== html) throw new Error(`drift: ${fileURLToPath(path)}; run npm run gen:kanban`);
  }
} finally {
  await server.close();
}
