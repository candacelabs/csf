import { describe, expect, it } from "vitest";
import {
  absoluteTime,
  authorLabel,
  initials,
  relativeTime,
  summarizeRequestPrompt,
  summarizeToolArgs,
} from "./format";

// Every case pins `now` rather than reading the clock, so none of these can go
// red at a particular time of day.
const now = new Date("2026-09-04T12:00:00Z");

describe("relativeTime", () => {
  it("reads recent moments as 'just now'", () => {
    expect(relativeTime("2026-09-04T11:59:31Z", now)).toBe("just now");
  });

  it("counts minutes, then hours, then days", () => {
    expect(relativeTime("2026-09-04T11:36:00Z", now)).toBe("24m ago");
    expect(relativeTime("2026-09-04T09:00:00Z", now)).toBe("3h ago");
    expect(relativeTime("2026-09-01T12:00:00Z", now)).toBe("3d ago");
  });

  it("names the day before rather than counting its hours", () => {
    expect(relativeTime("2026-09-03T09:00:00Z", now)).toBe("yesterday");
  });

  it("falls back to a date once relative wording stops helping", () => {
    expect(relativeTime("2026-07-01T12:00:00Z", now)).not.toContain("ago");
  });

  it("treats a clock skew into the future as the present", () => {
    expect(relativeTime("2026-09-04T12:00:03Z", now)).toBe("just now");
  });

  it("prints an unparseable timestamp back rather than 'Invalid Date'", () => {
    expect(relativeTime("not-a-time", now)).toBe("not-a-time");
    expect(absoluteTime("not-a-time")).toBe("not-a-time");
  });
});

describe("authorLabel", () => {
  it("prefers the group author when the event carried one", () => {
    expect(authorLabel("user", "Ada")).toBe("Ada");
  });

  it("names the roles when it did not", () => {
    expect(authorLabel("assistant")).toBe("Copilot");
    expect(authorLabel("user")).toBe("You");
    expect(authorLabel("system")).toBe("System");
    expect(authorLabel("user", "")).toBe("You");
  });
});

describe("initials", () => {
  it("takes one letter from one word and two from two", () => {
    expect(initials("Copilot")).toBe("C");
    expect(initials("Ada Lovelace")).toBe("AL");
  });

  it("never renders an empty avatar", () => {
    expect(initials("   ")).toBe("?");
  });
});

describe("summarizeRequestPrompt", () => {
  it("leaves a prompt written for a person exactly as it is", () => {
    expect(summarizeRequestPrompt("Run `rm -rf build`?")).toEqual({
      headline: "Run `rm -rf build`?",
      detail: null,
    });
  });

  it("leads with the command the CLI wants to run and keeps the payload", () => {
    const prompt = JSON.stringify({
      kind: "shell",
      commandSegments: [{ fullCommandText: "ls -1 /workspace", identifier: "ls" }],
      intention: "List the files",
    });
    const summary = summarizeRequestPrompt(prompt);
    expect(summary.headline).toBe("ls -1 /workspace");
    expect(summary.detail).toContain('"intention": "List the files"');
  });

  it("falls back through commands, then the stated intention", () => {
    expect(summarizeRequestPrompt('{"commands":[{"identifier":"git status"}]}').headline).toBe(
      "git status",
    );
    expect(summarizeRequestPrompt('{"intention":"Read a file"}').headline).toBe("Read a file");
  });

  it("shows a payload it cannot summarize rather than an empty card", () => {
    expect(summarizeRequestPrompt('{"unknown":1}')).toEqual({
      headline: '{"unknown":1}',
      detail: null,
    });
    expect(summarizeRequestPrompt("{not json")).toEqual({ headline: "{not json", detail: null });
  });
});

describe("summarizeToolArgs", () => {
  it("shows the value of a single-field argument object", () => {
    expect(summarizeToolArgs('{"command":"pytest -x"}')).toBe("pytest -x");
  });

  it("names the fields when there is more than one", () => {
    expect(summarizeToolArgs('{"path":"main.go","line":42}')).toBe("path=main.go line=42");
  });

  it("takes the first line of anything that is not JSON", () => {
    expect(summarizeToolArgs("ls -la\nsecond line")).toBe("ls -la");
    expect(summarizeToolArgs("   ")).toBe("");
  });
});
