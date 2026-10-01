import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Markdown, decodeEntities, safeHref } from "./markdown";

describe("Markdown", () => {
  it("renders a fenced block as a pre/code pair carrying its language", () => {
    const { container } = render(<Markdown source={"```python\nprint(1)\n```"} />);
    const code = container.querySelector("pre code");
    expect(code?.textContent).toBe("print(1)");
    expect(code?.getAttribute("data-language")).toBe("python");
  });

  it("renders emphasis, inline code and lists as elements rather than text", () => {
    const { container } = render(
      <Markdown source={"**bold** and `span`\n\n- one\n- two"} />,
    );
    expect(container.querySelector("strong")?.textContent).toBe("bold");
    expect(container.querySelector("code")?.textContent).toBe("span");
    expect(container.querySelectorAll("ul li")).toHaveLength(2);
  });

  // The whole sanitization argument in one case: marked is used as a lexer and
  // the tree becomes React elements, so a script tag in an answer is text.
  it("never turns raw HTML in an answer into markup", () => {
    const { container } = render(
      <Markdown source={'plain <script>alert("x")</script> tail'} />,
    );
    expect(container.querySelector("script")).toBeNull();
    expect(container.textContent).toContain('<script>alert("x")</script>');
  });

  it("keeps a javascript: link as its own text and links an http one", () => {
    const { container } = render(
      <Markdown source={"[bad](javascript:alert(1)) and [good](https://example.invalid/x)"} />,
    );
    const links = container.querySelectorAll("a");
    expect(links).toHaveLength(1);
    expect(links[0]?.getAttribute("href")).toBe("https://example.invalid/x");
    expect(links[0]?.getAttribute("rel")).toContain("noopener");
    expect(screen.getByText(/bad/)).toBeTruthy();
  });

  it("shows an image as its alt text rather than fetching from the named host", () => {
    const { container } = render(<Markdown source={"![a diagram](https://elsewhere.invalid/x.png)"} />);
    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toContain("a diagram");
  });

  it("renders a GFM table", () => {
    const { container } = render(
      <Markdown source={"| Case | Roots |\n| --- | --- |\n| d>0 | two |"} />,
    );
    expect(container.querySelectorAll("th")).toHaveLength(2);
    expect(container.querySelectorAll("tbody td")).toHaveLength(2);
  });
});

describe("safeHref", () => {
  it("accepts the three schemes a transcript link may use", () => {
    expect(safeHref("https://example.invalid")).toBe("https://example.invalid");
    expect(safeHref("http://example.invalid")).toBe("http://example.invalid");
    expect(safeHref("mailto:someone@example.invalid")).toBe("mailto:someone@example.invalid");
  });

  it("rejects the schemes that execute", () => {
    expect(safeHref("javascript:alert(1)")).toBeNull();
    expect(safeHref("data:text/html,<script>")).toBeNull();
  });
});

describe("decodeEntities", () => {
  it("undoes the lexer's escaping so React can do its own", () => {
    expect(decodeEntities("a &amp; b &lt;c&gt; &quot;d&quot; &#39;e&#39;")).toBe(
      "a & b <c> \"d\" 'e'",
    );
  });
});
