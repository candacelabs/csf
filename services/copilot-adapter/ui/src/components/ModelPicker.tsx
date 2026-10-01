import { useId } from "react";
import { ActionIcon, Group, NativeSelect, Stack } from "@mantine/core";
import type { Model } from "../api/client";

export type ModelPickerProps = {
  models: Model[];
  value: string;
  onChange: (model: string) => void;
  loading?: boolean;
  error?: string | null;
  disabled?: boolean;
  compact?: boolean;
  onRefresh?: () => void;
};

export function ModelPicker({ models, value, onChange, loading = false, error = null, disabled = false, compact = false, onRefresh }: ModelPickerProps) {
  const hintId = useId();
  const missingCurrent = value !== "" && !models.some((model) => model.id === value);
  const unavailable = error !== null || models.length === 0;
  const hint = loading ? "Loading models…"
    : error !== null ? `Model catalog unavailable: ${error}`
    : models.length === 0 ? "No models returned by the adapter."
    : missingCurrent ? "Current model is not in the available catalog."
    : `${models.length} ${models.length === 1 ? "model" : "models"} available`;

  const options = [
    { value: "", label: loading ? "Loading models…" : unavailable ? "Models unavailable" : "Choose a model", disabled: true },
    ...(missingCurrent ? [{ value, label: `${value} (current)`, disabled: true }] : []),
    ...models.map((model) => ({
      value: model.id,
      label: model.displayName.trim() === "" || model.displayName === model.id
        ? model.id
        : `${model.displayName} (${model.id})`,
    })),
  ];

  return (
    <Stack gap={2} miw={compact ? "9rem" : 0} w={compact ? "clamp(9rem, 18vw, 14rem)" : "100%"}>
      <NativeSelect
        label={compact ? undefined : "Model"}
        aria-label="Model"
        description={hint}
        descriptionProps={{ id: hintId, role: error !== null ? "alert" : undefined }}
        required
        size={compact ? "sm" : "md"}
        data={options}
        value={value}
        disabled={disabled || loading || unavailable}
        onChange={(event) => onChange(event.currentTarget.value)}
      />
      {onRefresh !== undefined && (
        <Group justify="flex-end">
          <ActionIcon
            type="button"
            variant="subtle"
            size="sm"
            aria-label="Refresh models"
            title="Refresh models"
            loading={loading}
            disabled={disabled || loading}
            onClick={onRefresh}
          >
            ↻
          </ActionIcon>
        </Group>
      )}
    </Stack>
  );
}
