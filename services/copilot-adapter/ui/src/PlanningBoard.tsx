import { useState } from "react";
import { ActionIcon, Badge, Group, NativeSelect, Paper, SimpleGrid, Stack, Text } from "@mantine/core";
import { useLocalStorage, useReducedMotion } from "@mantine/hooks";
import { DndContext, DragOverlay, KeyboardSensor, PointerSensor, closestCorners, useDroppable, useSensor, useSensors } from "@dnd-kit/core";
import { SortableContext, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import type { Session, Worktree } from "./api/client";
import { SessionCard } from "./SessionCard";
import { emptyPlan, movePlan, planningLaneIDs, planningLanes, reconcilePlan } from "./planning";
import type { PlanningLane } from "./planning";

export function PlanningBoardView({ sessions, matching, worktrees }: { sessions: Session[]; matching: Session[]; worktrees: Worktree[] }) {
  const [stored, setStored] = useLocalStorage<unknown>({ key: "csf-workbench-planning-v1", defaultValue: emptyPlan, getInitialValueInEffect: false });
  const [dragged, setDragged] = useState<string | null>(null);
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }), useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }));
  const ids = sessions.map((session) => session.id);
  const plan = reconcilePlan(stored, ids);
  const visible = new Map(matching.map((session) => [session.id, session]));
  function move(id: string, target: string) {
    setStored((previous: unknown) => movePlan(reconcilePlan(previous, ids), id, target));
  }
  return <DndContext sensors={sensors} collisionDetection={closestCorners}
    onDragStart={({ active }) => setDragged(String(active.id))} onDragCancel={() => setDragged(null)}
    onDragEnd={({ active, over }) => { setDragged(null); if (over) move(String(active.id), String(over.id)); }}>
    <SimpleGrid cols={{ base: 1, sm: 2, xl: 4 }} spacing="sm">
      {planningLaneIDs.map((lane) => <PlanningColumn key={lane} lane={lane} sessions={plan[lane].flatMap((id) => visible.has(id) ? [visible.get(id)!] : [])} worktrees={worktrees} onMove={move} />)}
    </SimpleGrid>
    <DragOverlay>{dragged ? <Paper withBorder shadow="md" radius="md" p="md" bg="orange.0"><Text fw={600}>{sessions.find((session) => session.id === dragged)?.displayName}</Text></Paper> : null}</DragOverlay>
  </DndContext>;
}

function PlanningColumn({ lane, sessions, worktrees, onMove }: { lane: PlanningLane; sessions: Session[]; worktrees: Worktree[]; onMove: (id: string, target: string) => void }) {
  const { setNodeRef, isOver } = useDroppable({ id: lane });
  return <Paper ref={setNodeRef} component="section" aria-label={`${planningLanes[lane]} planning`} withBorder radius="md" p="sm" bg={isOver ? "orange.0" : "gray.0"} mih={180}>
    <Stack gap="sm"><Group justify="space-between"><Text fw={700} size="sm">{planningLanes[lane]}</Text><Badge variant="light" color="orange">{sessions.length}</Badge></Group>
      <SortableContext items={sessions.map((session) => session.id)} strategy={verticalListSortingStrategy}>
        {sessions.map((session) => <PlanningCard key={session.id} session={session} lane={lane} worktree={worktrees.find((worktree) => worktree.id === session.worktreeId)} onMove={onMove} />)}
      </SortableContext>
      {sessions.length === 0 && <Text size="xs" c="dimmed" py="lg">Drop a card here</Text>}
    </Stack>
  </Paper>;
}

function PlanningCard({ session, worktree, lane, onMove }: { session: Session; worktree: Worktree | undefined; lane: PlanningLane; onMove: (id: string, target: string) => void }) {
  const { attributes, listeners, setNodeRef, setActivatorNodeRef, transform, transition, isDragging } = useSortable({ id: session.id });
  const reducedMotion = useReducedMotion();
  return <Stack ref={setNodeRef} gap={4} style={{ transform: CSS.Transform.toString(transform), transition: reducedMotion ? undefined : transition, opacity: isDragging ? 0.35 : 1 }}>
    <Group wrap="nowrap" gap="xs"><ActionIcon ref={setActivatorNodeRef} {...attributes} {...listeners} aria-label={`Drag ${session.displayName}`} variant="subtle" color="gray" size="lg" style={{ cursor: "grab", touchAction: "none" }}>⠿</ActionIcon>
      <NativeSelect aria-label={`Move ${session.displayName}`} value={lane} onChange={(event) => onMove(session.id, event.currentTarget.value)} size="xs" style={{ flex: 1 }} data={planningLaneIDs.map((value) => ({ value, label: planningLanes[value] }))} />
    </Group>
    <SessionCard session={session} worktree={worktree} />
  </Stack>;
}
