// @vitest-environment node

import { createServer as createHTTPServer } from "node:http";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createServer as createViteServer, mergeConfig } from "vite";
import { expect, it } from "vitest";

import adapterConfig from "../vite.config";

function listen(server) {
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      server.off("error", reject);
      resolve();
    });
  });
}

function close(server) {
  return new Promise((resolve, reject) => {
    server.close((error) => {
      if (error) {
        reject(error);
        return;
      }
      resolve();
    });
  });
}

function portOf(server) {
  const address = server.address();
  if (address === null || typeof address === "string") {
    throw new Error("server did not bind a TCP port");
  }
  return address.port;
}

it("does not expose the loopback API proxy to a malicious browser origin", async () => {
  const backend = createHTTPServer((_request, response) => {
    response.setHeader("content-type", "application/json");
    response.end(JSON.stringify({ marker: "private adapter response" }));
  });
  await listen(backend);

  const cacheDir = await mkdtemp(join(tmpdir(), "copilot-adapter-vite-"));
  const server = await createViteServer(
    mergeConfig(adapterConfig, {
      cacheDir,
      configFile: false,
      logLevel: "silent",
      server: {
        host: "127.0.0.1",
        port: 0,
        proxy: {
          "/v1": {
            changeOrigin: false,
            target: `http://127.0.0.1:${portOf(backend)}`,
          },
        },
      },
    }),
  );

  try {
    await server.listen();
    const origin = "https://attacker.example";
    const url = `http://127.0.0.1:${portOf(server.httpServer)}/v1/private`;

    const response = await fetch(url, { headers: { origin } });
    expect(response.status).toBe(200);
    expect(await response.json()).toEqual({ marker: "private adapter response" });
    expect(response.headers.get("access-control-allow-origin")).toBeNull();

    const preflight = await fetch(url, {
      method: "OPTIONS",
      headers: {
        origin,
        "access-control-request-headers": "content-type",
        "access-control-request-method": "POST",
      },
    });
    expect(preflight.headers.get("access-control-allow-origin")).toBeNull();
  } finally {
    await server.close();
    await close(backend);
    await rm(cacheDir, { force: true, recursive: true });
  }
});
