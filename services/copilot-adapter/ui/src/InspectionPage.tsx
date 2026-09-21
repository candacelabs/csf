import type { ReactNode, RefObject } from "react";
import { Box, Button, Container, Group, Paper, Stack, Text, Title } from "@mantine/core";

export type InspectionPageProps = {
  menuButtonRef: RefObject<HTMLButtonElement>;
  onMenu: () => void;
};

export function InspectionPage({ title, description, actions, children, menuButtonRef, onMenu }: InspectionPageProps & {
  title: string; description: string; actions?: ReactNode; children: ReactNode;
}) {
  return <Box w="100%" h="100%" bg="gray.0" style={{ overflowY: "auto" }}>
    <Container size="xl" py="xl" px={{ base: "md", sm: "xl" }}>
      <Stack gap="xl">
        <Group justify="space-between">
          <Group gap="xs"><Button ref={menuButtonRef} hiddenFrom="sm" variant="subtle" aria-label="Open task sidebar" onClick={onMenu}>☰</Button>
            <Text fw={700} size="sm" c="orange">CSF · The Cerebrospinal Fluid</Text></Group>
          <Button component="a" href="#/" variant="default" size="xs">Home overview</Button>
        </Group>
        <Paper withBorder radius="lg" p={{ base: "lg", sm: "xl" }} bg="orange.0">
          <Stack gap="sm"><Title order={1}>{title}</Title><Text c="dimmed" maw={720}>{description}</Text>{actions}</Stack>
        </Paper>
        {children}
      </Stack>
    </Container>
  </Box>;
}
