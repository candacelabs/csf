import { Center, Group, Loader, Text } from "@mantine/core";

export function PaneLoading({ label }: { label: string }) {
  return <Center className="pane-loading"><Group gap="sm"><Loader size="sm" color="orange" /><Text c="dimmed">{label}…</Text></Group></Center>;
}
