import { useEffect, useState } from "react";
import { csf } from "./api/csf";

const refreshIntervalMilliseconds = 5_000;

function usesDefaultTheme(search: string): boolean {
  return new URLSearchParams(search).get("theme") === "default";
}

export function WorkbenchThemeOverride() {
  const [customCss, setCustomCss] = useState("");
  const bypass = usesDefaultTheme(window.location.search);

  useEffect(() => {
    if (bypass) return;

    const controller = new AbortController();
    let loading = false;

    async function refreshTheme() {
      if (loading || controller.signal.aborted) return;
      loading = true;
      try {
        const { data, error } = await csf.POST("/api/workbench/theme/get", {
          body: {},
          signal: controller.signal,
        });
        if (controller.signal.aborted || error !== undefined || data === undefined) return;
        setCustomCss(data.theme?.customCss ?? "");
      } catch {
        // Keep the last successful CSS when the shared theme cannot be read.
      } finally {
        loading = false;
      }
    }

    const refreshWhenVisible = () => {
      if (document.visibilityState === "visible") void refreshTheme();
    };

    void refreshTheme();
    const timer = window.setInterval(refreshWhenVisible, refreshIntervalMilliseconds);
    window.addEventListener("focus", refreshWhenVisible);
    document.addEventListener("visibilitychange", refreshWhenVisible);

    return () => {
      controller.abort();
      window.clearInterval(timer);
      window.removeEventListener("focus", refreshWhenVisible);
      document.removeEventListener("visibilitychange", refreshWhenVisible);
    };
  }, [bypass]);

  if (bypass || customCss === "") return null;
  return <style data-workbench-theme>{customCss}</style>;
}
