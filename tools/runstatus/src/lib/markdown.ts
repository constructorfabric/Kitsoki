/**
 * Agent-message markdown rendering for the main operator chat.
 *
 * The engine ships ALREADY-RENDERED text (an 80-col terminal room view, or a
 * streamed agent reply) — the browser never evaluates pongo. We only apply a
 * light, HTML-safe markdown pass on top:
 *
 *   - fenced code blocks (```lang\n…\n```) → a styled <pre><code> box. This is
 *     what stops a raw ```json reply from leaking to the operator as literal
 *     backtick-fenced text.
 *   - ATX heading lines (## Title) → a bold heading span (markers stripped).
 *   - inline `code` and **bold**.
 *
 * Everything OUTSIDE a fence is kept verbatim, line by line: newlines,
 * indentation and column alignment survive (the caller pairs this with
 * white-space: pre-wrap), so engine-laid-out lists / key:value tables are never
 * re-flowed into run-on prose. Only the fenced segments are reflowed into the
 * code box. An UNCLOSED fence (mid-stream, before the closing ``` arrives) is
 * left as plain escaped text and snaps into a code box once it closes.
 *
 * The source is HTML-escaped FIRST (it can embed user-supplied idea text), so
 * the result is safe to inject with v-html.
 */

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

// renderInline applies bold + inline-code to an ALREADY HTML-escaped string.
function renderInline(escaped: string): string {
  return escaped
    .replace(/`([^`]+)`/g, "<code>$1</code>")
    .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
}

/**
 * Full markdown document renderer for local .md files (proposal briefs, etc.).
 * Handles: ATX headings, fenced code blocks, blockquotes, bullet/ordered lists,
 * horizontal rules, inline code, bold, italic, links, and paragraph breaks.
 * The output is safe HTML (all literal text is entity-escaped before inline
 * processing so no user content can inject tags).
 */
export function renderMarkdownDocument(src: string): string {
  const lines = (src ?? "").replace(/\r\n/g, "\n").split("\n");
  const out: string[] = [];
  let i = 0;

  // Collect lines until a blank line or end.
  function peekParagraph(): string[] {
    const buf: string[] = [];
    let j = i;
    while (j < lines.length && lines[j].trim() !== "") {
      buf.push(lines[j]);
      j++;
    }
    return buf;
  }

  while (i < lines.length) {
    const line = lines[i];

    // Fenced code block (``` or ~~~)
    const fenceMatch = line.match(/^(`{3,}|~{3,})(.*)/);
    if (fenceMatch) {
      const fence = fenceMatch[1];
      const lang = fenceMatch[2].trim();
      const langAttr = lang ? ` class="language-${escapeHtml(lang)}"` : "";
      const body: string[] = [];
      i++;
      while (i < lines.length && !lines[i].startsWith(fence)) {
        body.push(lines[i]);
        i++;
      }
      i++; // consume closing fence
      out.push(
        `<pre class="md-pre"><code${langAttr}>${escapeHtml(body.join("\n"))}</code></pre>`
      );
      continue;
    }

    // ATX heading
    const headingMatch = line.match(/^(#{1,6})\s+(.*)/);
    if (headingMatch) {
      const level = headingMatch[1].length;
      out.push(`<h${level} class="md-h${level}">${renderInlineDoc(headingMatch[2])}</h${level}>`);
      i++;
      continue;
    }

    // Horizontal rule
    if (/^[-*_]{3,}\s*$/.test(line)) {
      out.push(`<hr class="md-hr" />`);
      i++;
      continue;
    }

    // GitHub-style pipe table. Keep this before paragraph handling so a table
    // block is not collapsed into prose.
    if (looksLikeTableStart(lines, i)) {
      const header = splitTableRow(lines[i]);
      const align = splitTableRow(lines[i + 1]).map(tableAlign);
      const body: string[][] = [];
      i += 2;
      while (i < lines.length && isTableRow(lines[i])) {
        body.push(splitTableRow(lines[i]));
        i++;
      }
      out.push(renderTable(header, align, body));
      continue;
    }

    // Blockquote
    if (line.startsWith("> ") || line === ">") {
      const blockLines: string[] = [];
      while (i < lines.length && (lines[i].startsWith("> ") || lines[i] === ">")) {
        blockLines.push(lines[i].replace(/^>\s?/, ""));
        i++;
      }
      out.push(`<blockquote class="md-blockquote">${renderMarkdownDocument(blockLines.join("\n"))}</blockquote>`);
      continue;
    }

    // Unordered list
    if (/^[*\-+]\s/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^[*\-+]\s/.test(lines[i])) {
        items.push(`<li>${renderInlineDoc(lines[i].replace(/^[*\-+]\s/, ""))}</li>`);
        i++;
      }
      out.push(`<ul class="md-ul">${items.join("")}</ul>`);
      continue;
    }

    // Ordered list
    if (/^\d+\.\s/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\d+\.\s/.test(lines[i])) {
        items.push(`<li>${renderInlineDoc(lines[i].replace(/^\d+\.\s/, ""))}</li>`);
        i++;
      }
      out.push(`<ol class="md-ol">${items.join("")}</ol>`);
      continue;
    }

    // Blank line — skip
    if (line.trim() === "") {
      i++;
      continue;
    }

    // Paragraph — consume until blank line
    const paraLines = peekParagraph();
    i += paraLines.length;
    out.push(`<p class="md-p">${renderInlineDoc(paraLines.join(" "))}</p>`);
  }

  return out.join("\n");
}

function looksLikeTableStart(lines: string[], idx: number): boolean {
  if (idx + 1 >= lines.length) return false;
  return isTableRow(lines[idx]) && isTableSeparator(lines[idx + 1]);
}

function isTableRow(line: string): boolean {
  return line.includes("|") && line.trim() !== "";
}

function isTableSeparator(line: string): boolean {
  const cells = splitTableRow(line);
  if (cells.length < 2) return false;
  return cells.every((cell) => /^:?-{3,}:?$/.test(cell.trim()));
}

function splitTableRow(line: string): string[] {
  let s = line.trim();
  if (s.startsWith("|")) s = s.slice(1);
  if (s.endsWith("|")) s = s.slice(0, -1);
  return s.split("|").map((cell) => cell.trim());
}

function tableAlign(cell: string): string {
  const trimmed = cell.trim();
  if (trimmed.startsWith(":") && trimmed.endsWith(":")) return "center";
  if (trimmed.endsWith(":")) return "right";
  if (trimmed.startsWith(":")) return "left";
  return "";
}

function renderTable(header: string[], align: string[], body: string[][]): string {
  const alignAttr = (idx: number) =>
    align[idx] ? ` style="text-align:${align[idx]}"` : "";
  const heads = header
    .map((cell, idx) => `<th${alignAttr(idx)}>${renderInlineDoc(cell)}</th>`)
    .join("");
  const rows = body
    .map((row) => {
      const cells = header
        .map((_, idx) => `<td${alignAttr(idx)}>${renderInlineDoc(row[idx] ?? "")}</td>`)
        .join("");
      return `<tr>${cells}</tr>`;
    })
    .join("");
  return `<table class="md-table"><thead><tr>${heads}</tr></thead><tbody>${rows}</tbody></table>`;
}

/** Inline markdown: bold, italic, code, links — applied to already-escaped text. */
function renderInlineDoc(raw: string): string {
  // Escape HTML first, then apply inline patterns.
  let s = escapeHtml(raw);
  // Inline code (backtick): replace in a single pass to avoid re-scanning.
  s = s.replace(/`([^`]+)`/g, "<code>$1</code>");
  // Bold+italic
  s = s.replace(/\*\*\*([^*]+)\*\*\*/g, "<strong><em>$1</em></strong>");
  // Bold
  s = s.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  s = s.replace(/__([^_]+)__/g, "<strong>$1</strong>");
  // Italic
  s = s.replace(/\*([^*]+)\*/g, "<em>$1</em>");
  s = s.replace(/_([^_]+)_/g, "<em>$1</em>");
  // Links [text](url)
  s = s.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank" rel="noopener">$1</a>');
  return s;
}

export function renderAgentMarkdown(src: string): string {
  const text = src ?? "";
  // Split into fenced-block segments (odd indices) and plain segments (even).
  const parts = text.split(/(```[^\n]*\n[\s\S]*?```)/g);
  return parts
    .map((part, idx) => {
      if (idx % 2 === 1) {
        const match = part.match(/^```([^\n]*)\n([\s\S]*?)```$/);
        const body = match ? match[2] : part.slice(3, -3);
        const lang = match?.[1]?.trim() ?? "";
        const langAttr = lang ? ` class="language-${escapeHtml(lang)}"` : "";
        return `<pre class="cv-pre"><code${langAttr}>${escapeHtml(body)}</code></pre>`;
      }
      // Plain text: keep verbatim line structure, format headings + inline.
      return escapeHtml(part)
        .split("\n")
        .map((line) => {
          const h = line.match(/^(#{1,6})\s+(.*)$/);
          if (h) return `<span class="cv-h">${renderInline(h[2])}</span>`;
          return renderInline(line);
        })
        .join("\n");
    })
    .join("");
}
