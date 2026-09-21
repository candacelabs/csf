import { lazy, Suspense } from "react";
import { ActionIcon, Badge, Group, Tabs, Text } from "@mantine/core";
import { useMediaQuery } from "@mantine/hooks";
import type { Subagent, SubagentActivity } from "../api/client";
import { PaneLoading } from "./PaneLoading";

const TerminalPane = lazy(async () => ({ default: (await import("./TerminalPane")).TerminalPane }));
const ChangesPane = lazy(async () => ({ default: (await import("./ChangesPane")).ChangesPane }));
const WorktreePane = lazy(async () => ({ default: (await import("./WorktreePane")).WorktreePane }));
const SchedulesPane = lazy(async () => ({ default: (await import("./SchedulesPane")).SchedulesPane }));
const SubagentsPane = lazy(async () => ({ default: (await import("./SubagentsPane")).SubagentsPane }));

export type DockTab = "terminal" | "changes" | "worktree" | "schedules" | "subagents";
export type DockPosition = "right" | "bottom";

const tabs: Array<{ id: DockTab; label: string; icon: string }> = [
  { id: "terminal", label: "Terminal", icon: ">_" },
  { id: "changes", label: "Changes", icon: "±" },
  { id: "worktree", label: "Worktree", icon: "↳" },
  { id: "schedules", label: "Schedules", icon: "◷" },
  { id: "subagents", label: "Subagents", icon: "☀" },
];

type DockProps = {
  active: DockTab;
  open: boolean;
  position: DockPosition;
  sessionId: string;
  worktreeId: string;
  revision: number;
  subagents: Subagent[];
  liveActivity: Record<string, SubagentActivity[]>;
  onTab: (tab: DockTab) => void;
  onOpenChange: (open: boolean) => void;
  onPosition: (position: DockPosition) => void;
  onSubagentsLoaded: (subagents: Subagent[]) => void;
};

export function Dock({ active, open, position, sessionId, worktreeId, revision, subagents, liveActivity, onTab, onOpenChange, onPosition, onSubagentsLoaded }: DockProps) {
  const activeAgents = subagents.filter((subagent) => subagent.status === "active").length;
  const desktop = useMediaQuery("(min-width: 901px)");
  const vertical = desktop && position === "right" && !open;
  return (
    <aside className={`dock dock-${position}${open ? "" : " closed"}`} aria-label="Workbench tools">
      <Tabs value={open ? active : null} orientation={vertical ? "vertical" : "horizontal"}
        onChange={(value) => {
          const tab = tabs.find((candidate) => candidate.id === value);
          if (tab) { onTab(tab.id); onOpenChange(true); }
        }} style={{ display: "flex", flexDirection: "column", minHeight: 0, height: "100%" }}>
      <header className="dock-header">
        <Tabs.List className="dock-tabs" aria-label="Workbench panes" style={{ flexWrap: "nowrap" }}>
          {tabs.map((tab) => (
            <Tabs.Tab
              key={tab.id}
              value={tab.id}
              aria-label={tab.label}
              title={tab.label}
              px={vertical ? "xs" : "sm"}
              leftSection={<Text span size="sm" aria-hidden="true">{tab.icon}</Text>}
              rightSection={tab.id === "subagents" && activeAgents > 0 ? <Badge size="xs" variant="light">{activeAgents}</Badge> : undefined}
              onFocus={(event) => event.currentTarget.scrollIntoView?.({ block: "nearest", inline: "nearest" })}
            >
              {!vertical && tab.label}
            </Tabs.Tab>
          ))}
        </Tabs.List>
        <Group className="dock-controls" gap={4} wrap="nowrap">
          <ActionIcon variant="subtle" color="gray" onClick={() => onPosition(position === "right" ? "bottom" : "right")} aria-label={position === "right" ? "Dock pane at bottom" : "Dock pane at right"} title={position === "right" ? "Dock bottom" : "Dock right"}>{position === "right" ? "▱" : "▥"}</ActionIcon>
          <ActionIcon variant="subtle" color="gray" onClick={() => onOpenChange(!open)} aria-label={open ? "Collapse pane" : "Expand pane"} title={open ? "Collapse pane" : "Expand pane"}>{open ? "—" : "□"}</ActionIcon>
        </Group>
      </header>
      {open && (
        <Tabs.Panel value={active} className="dock-body">
          <Suspense fallback={<PaneLoading label="Opening pane" />}>
            {active === "terminal" && <TerminalPane worktreeId={worktreeId} />}
            {active === "changes" && <ChangesPane worktreeId={worktreeId} revision={revision} />}
            {active === "worktree" && <WorktreePane worktreeId={worktreeId} revision={revision} />}
            {active === "schedules" && <SchedulesPane sessionId={sessionId} />}
            {active === "subagents" && <SubagentsPane sessionId={sessionId} subagents={subagents} liveActivity={liveActivity} onLoaded={onSubagentsLoaded} />}
          </Suspense>
        </Tabs.Panel>
      )}
      </Tabs>
    </aside>
  );
}
