import { Button, Card, Stack, Text } from "@mantine/core";
import { csfURL } from "./api/csf";
import { InspectionPage } from "./InspectionPage";
import type { InspectionPageProps } from "./InspectionPage";

export function ReleasePage(props: InspectionPageProps) {
  const guide = import.meta.env.VITE_CSF_RELEASE_URL;
  return <InspectionPage {...props} title="Release & evidence" description="Release information supplied by this installation.">
    <Card withBorder radius="lg" p="xl"><Stack gap="md">
      <Text>Consult the release guide for the source version, verification receipts and supported configuration.</Text>
      {guide ? <Button component="a" href={csfURL(guide)}>Configured consumer guide ↗</Button> : <Text c="dimmed">No release guide is configured for this installation.</Text>}
    </Stack></Card>
  </InspectionPage>;
}
