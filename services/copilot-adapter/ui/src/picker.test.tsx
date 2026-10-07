import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { describe, expect, it, vi } from "vitest";
import { Markdown } from "./markdown";
import { PickerHostContext, parsePicker, pickerReply } from "./picker";
import type { PickerHost } from "./picker";

const papers = JSON.stringify({
  title: "Papers",
  options: [
    { id: "rrsi", label: "RRSI", detail: "Overfitting guard", url: "https://example.invalid/rrsi" },
    { id: "neo", label: "NeoHorse-1", url: "javascript:alert(1)" },
    "Plain option",
  ],
});

function withHost(source: string, host: PickerHost) {
  return render(
    <MantineProvider>
      <PickerHostContext.Provider value={host}>
        <Markdown source={source} />
      </PickerHostContext.Provider>
    </MantineProvider>,
  );
}

describe("parsePicker", () => {
  it("accepts objects and bare strings and defaults the choices", () => {
    const spec = parsePicker(papers);
    expect(spec?.choices).toEqual(["Explore", "Skip"]);
    expect(spec?.options.map((option) => option.id)).toEqual(["rrsi", "neo", "option-2"]);
  });

  it("rejects partial, empty and inconsistent pickers", () => {
    expect(parsePicker('{"title": "half", "options": [')).toBeNull();
    expect(parsePicker('{"options": []}')).toBeNull();
    expect(parsePicker('{"options": [{"id": "a", "label": "x"}, {"id": "a", "label": "y"}]}')).toBeNull();
    expect(parsePicker('{"options": ["x"], "choices": ["Only"]}')).toBeNull();
    expect(parsePicker('{"options": ["x"], "choices": ["Same", "Same"]}')).toBeNull();
  });
});

describe("pickerReply", () => {
  it("groups labels by choice and lists the undecided", () => {
    const spec = parsePicker(papers)!;
    expect(pickerReply(spec, { rrsi: "Explore", neo: "Skip" })).toBe(
      'Choices for "Papers":\n- Explore: RRSI\n- Skip: NeoHorse-1\n- Undecided: Plain option',
    );
  });
});

describe("picker in Markdown", () => {
  it("stays a code block without a session host", () => {
    const { container } = render(<Markdown source={"```csf-picker\n" + papers + "\n```"} />);
    expect(container.querySelector('code[data-language="csf-picker"]')).not.toBeNull();
    expect(container.querySelector("button")).toBeNull();
  });

  it("renders buttons, links only safe URLs and sends the decisions", async () => {
    const submit = vi.fn(async () => true);
    const { container } = withHost("Pick:\n\n```csf-picker\n" + papers + "\n```", { submit, disabled: false });

    const send = screen.getByRole("button", { name: "Send choices" });
    expect(send.hasAttribute("disabled")).toBe(true);
    expect(container.querySelectorAll("a")).toHaveLength(1);
    expect(container.querySelector("a")?.getAttribute("href")).toBe("https://example.invalid/rrsi");

    fireEvent.click(screen.getAllByRole("button", { name: "Explore" })[0]!);
    fireEvent.click(screen.getAllByRole("button", { name: "Skip" })[1]!);
    expect(screen.getAllByRole("button", { name: "Explore" })[0]!.getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(send);

    await waitFor(() => expect(submit).toHaveBeenCalledTimes(1));
    expect(submit).toHaveBeenCalledWith('Choices for "Papers":\n- Explore: RRSI\n- Skip: NeoHorse-1\n- Undecided: Plain option');
    expect(await screen.findByText("Sent")).toBeTruthy();
  });

  it("toggles a choice off and disables sending while the session cannot accept a prompt", () => {
    withHost("```csf-picker\n" + papers + "\n```", { submit: vi.fn(async () => true), disabled: true });
    const explore = screen.getAllByRole("button", { name: "Explore" })[0]!;
    fireEvent.click(explore);
    fireEvent.click(explore);
    expect(explore.getAttribute("aria-pressed")).toBe("false");
    fireEvent.click(screen.getByRole("button", { name: "All Explore" }));
    expect(screen.getByText("3/3 decided")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Send choices" }).hasAttribute("disabled")).toBe(true);
  });
});
