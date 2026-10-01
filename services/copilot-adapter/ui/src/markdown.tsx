import { Fragment } from "react";
import { marked } from "marked";
import type { Token, Tokens } from "marked";

// Assistant text is Markdown. marked does the parsing — a hand-rolled parser
// would be the wrong thing to own — but it is used as a LEXER only: the token
// tree is rendered into React elements here, never into an HTML string.
//
// That is the sanitization story, and it is structural rather than a filter
// pass: no path in this file reaches dangerouslySetInnerHTML, so raw HTML in a
// model's answer can only ever become visible text. The two decisions a
// sanitizer would otherwise make are made explicitly instead — a link's scheme
// is checked before it becomes an href, and an image becomes its alt text
// rather than a request to somebody else's server.

const SAFE_SCHEMES = ["http:", "https:", "mailto:"];

// A link is a link only if it goes somewhere a browser may safely follow;
// javascript:, data: and vbscript: hrefs render as their own text instead.
export function safeHref(href: string): string | null {
  try {
    const parsed = new URL(href, "http://adapter.invalid/");
    return SAFE_SCHEMES.includes(parsed.protocol) ? href : null;
  } catch {
    return null;
  }
}

// marked hands back HTML-escaped text in `text` and `codespan` tokens. React
// escapes on output, so the entities have to come back off first or the reader
// sees `&amp;` where the model wrote `&`.
export function decodeEntities(text: string): string {
  return text
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&amp;/g, "&");
}

function renderInline(tokens: Token[] | undefined): React.ReactNode {
  if (tokens === undefined) return null;
  return tokens.map((token, index) => {
    const key = `${token.type}-${index}`;
    switch (token.type) {
      case "strong":
        return <strong key={key}>{renderInline((token as Tokens.Strong).tokens)}</strong>;
      case "em":
        return <em key={key}>{renderInline((token as Tokens.Em).tokens)}</em>;
      case "del":
        return <del key={key}>{renderInline((token as Tokens.Del).tokens)}</del>;
      case "codespan":
        return <code key={key}>{decodeEntities((token as Tokens.Codespan).text)}</code>;
      case "br":
        return <br key={key} />;
      case "link": {
        const link = token as Tokens.Link;
        const href = safeHref(link.href);
        const body = renderInline(link.tokens);
        if (href === null) return <Fragment key={key}>{body}</Fragment>;
        return (
          <a key={key} href={href} target="_blank" rel="noreferrer noopener">
            {body}
          </a>
        );
      }
      case "image":
        // Rendering the tag would fetch from whatever host the answer named.
        // The alt text carries the meaning without the request.
        return <em key={key}>{(token as Tokens.Image).text}</em>;
      case "escape":
        return <Fragment key={key}>{(token as Tokens.Escape).text}</Fragment>;
      default:
        // text, html and anything the lexer adds later: visible text, never
        // markup.
        return <Fragment key={key}>{decodeEntities(token.raw)}</Fragment>;
    }
  });
}

function renderListItem(item: Tokens.ListItem, key: string): React.ReactNode {
  return <li key={key}>{renderBlocks(item.tokens)}</li>;
}

function renderBlocks(tokens: Token[] | undefined): React.ReactNode {
  if (tokens === undefined) return null;
  return tokens.map((token, index) => {
    const key = `${token.type}-${index}`;
    switch (token.type) {
      case "space":
        return null;
      case "paragraph":
        return <p key={key}>{renderInline((token as Tokens.Paragraph).tokens)}</p>;
      case "heading": {
        const heading = token as Tokens.Heading;
        const body = renderInline(heading.tokens);
        if (heading.depth <= 1) return <h1 key={key}>{body}</h1>;
        if (heading.depth === 2) return <h2 key={key}>{body}</h2>;
        return <h3 key={key}>{body}</h3>;
      }
      case "code": {
        const code = token as Tokens.Code;
        return (
          <pre key={key} style={{ maxHeight: 320, overflow: "auto", overflowWrap: "anywhere", whiteSpace: "pre-wrap" }}>
            <code data-language={code.lang ?? ""}>{code.text}</code>
          </pre>
        );
      }
      case "blockquote":
        return (
          <blockquote key={key}>{renderBlocks((token as Tokens.Blockquote).tokens)}</blockquote>
        );
      case "list": {
        const list = token as Tokens.List;
        const items = list.items.map((item, at) => renderListItem(item, `item-${at}`));
        if (!list.ordered) return <ul key={key} style={{ paddingInlineStart: "1.5em" }}>{items}</ul>;
        const start = typeof list.start === "number" ? list.start : 1;
        return (
          <ol key={key} start={start} style={{ paddingInlineStart: "1.5em" }}>
            {items}
          </ol>
        );
      }
      case "table": {
        const table = token as Tokens.Table;
        return (
          <table key={key}>
            <thead>
              <tr>
                {table.header.map((cell, at) => (
                  <th key={`h-${at}`}>{renderInline(cell.tokens)}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {table.rows.map((row, rowAt) => (
                <tr key={`r-${rowAt}`}>
                  {row.map((cell, cellAt) => (
                    <td key={`c-${cellAt}`}>{renderInline(cell.tokens)}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        );
      }
      case "hr":
        return <hr key={key} />;
      case "text": {
        const text = token as Tokens.Text;
        return (
          <p key={key}>
            {text.tokens === undefined ? decodeEntities(text.text) : renderInline(text.tokens)}
          </p>
        );
      }
      default:
        // html, def and anything unhandled: shown as the text it literally is.
        return <p key={key}>{decodeEntities(token.raw)}</p>;
    }
  });
}

export type MarkdownProps = { source: string };

// The one Markdown surface in the app.
export function Markdown({ source }: MarkdownProps) {
  const tokens = marked.lexer(source, { gfm: true, breaks: true });
  return <div className="md" style={{ overflowWrap: "anywhere" }}>{renderBlocks(tokens)}</div>;
}
