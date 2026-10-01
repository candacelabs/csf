// @vitest-environment node
import { describe, expect, it } from "vitest";
import { renderKanbanTemplates } from "./kanbanTemplates";

describe("Mantine to Go template generation", () => {
  it("keeps adjacent conditional markers separate when rendering task links", () => {
    const card = renderKanbanTemplates()["card_cgen.html"];
    expect(card).toContain('</a>{{end}}{{if .CheckpointURL}}<a');
    expect(card).toContain('</a>{{end}}{{if .EvidenceURL}}<a');
    expect(card).not.toContain('.end____');
  });

  it("resolves every marker without leaking one into a Go field expression", () => {
    for (const html of Object.values(renderKanbanTemplates())) {
      expect(html).not.toContain('__KANBAN_');
      expect(html).not.toMatch(/\{\{\.[^}]*__/);
    }
  });
});
