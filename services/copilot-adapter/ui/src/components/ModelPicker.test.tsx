import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ModelPicker } from "./ModelPicker";
import type { Model } from "../api/client";
import { renderWithMantine as render } from "../test-utils";

const models: Model[] = [
  { id: "model-a", displayName: "Model A", capabilities: ["chat"] },
  { id: "model-b", displayName: "Model B", capabilities: ["chat"] },
];

describe("ModelPicker", () => {
  it("shows live display names and IDs and sends the selected catalog ID", () => {
    const change = vi.fn();
    render(<ModelPicker models={models} value="model-a" onChange={change} />);
    expect(screen.getByRole("option", { name: "Model A (model-a)" })).toBeTruthy();
    expect(screen.getByRole("option", { name: "Model B (model-b)" })).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Model"), { target: { value: "model-b" } });
    expect(change).toHaveBeenCalledWith("model-b");
  });

  it("preserves an unlisted current model instead of displaying the first option", () => {
    render(<ModelPicker models={models} value="older-model" onChange={() => undefined} />);
    expect((screen.getByLabelText("Model") as HTMLSelectElement).value).toBe("older-model");
    expect(screen.getByRole("option", { name: "older-model (current)", selected: true })).toBeTruthy();
    expect(screen.getByText("Current model is not in the available catalog.")).toBeTruthy();
  });

  it.each([
    [{ loading: true }, "Loading models…"],
    [{ error: "CLI unavailable" }, "Model catalog unavailable: CLI unavailable"],
    [{}, "No models returned by the adapter."],
  ])("disables catalog selection while unavailable and keeps the current model (%j)", (state, hint) => {
    render(<ModelPicker models={[]} value="current-model" onChange={() => undefined} {...state} />);
    const picker = screen.getByLabelText("Model") as HTMLSelectElement;
    expect(picker.disabled).toBe(true);
    expect(picker.value).toBe("current-model");
    expect(document.getElementById(picker.getAttribute("aria-describedby") ?? "")?.textContent).toBe(hint);
  });

  it("does not invent other models when the adapter only offers Auto", () => {
    const refresh = vi.fn();
    render(<ModelPicker models={[{ id: "auto", displayName: "Auto", capabilities: [] }]} value="auto" onChange={() => undefined} onRefresh={refresh} />);
    expect(screen.getAllByRole("option")).toHaveLength(2);
    expect(screen.getByRole("option", { selected: true }).textContent).toBe("Auto (auto)");
    expect(screen.getByText("1 model available")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Refresh models" }));
    expect(refresh).toHaveBeenCalledOnce();
  });
});
