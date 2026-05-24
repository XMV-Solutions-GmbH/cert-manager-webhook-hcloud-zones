<!-- SPDX-License-Identifier: MIT OR Apache-2.0 -->
# Markdown style guide

> **Read this only when you are producing or editing Markdown.** When you're working on code that doesn't touch `.md` files, this content is just noise — skip it. The split exists because LLMs are unreliable about Markdown lint and dumping these rules into every context window is wasteful.

The rules below match `.markdownlint.yaml` at the repo root, which is what CI's lint job (`DavidAnson/markdownlint-cli2-action`) enforces. CI will reject violations.

## Required rules

| Rule | Description | Example |
|---|---|---|
| **MD001** | Heading levels increment by one | ✅ `#` → `##` → `###`  ❌ `#` → `###` |
| **MD003** | Consistent heading style | Use ATX style (`#`) |
| **MD004** | Consistent list marker | Use `-` for unordered lists |
| **MD009** | No trailing spaces | Trim all trailing whitespace |
| **MD010** | No hard tabs | Use spaces for indentation |
| **MD012** | No multiple consecutive blank lines | Maximum one blank line |
| **MD022** | Blank line before and after headings | Always add blank lines |
| **MD031** | Blank line before and after fenced code blocks | Always add blank lines |
| **MD032** | Blank line before and after lists | Always add blank lines |
| **MD033** | No inline HTML (unless necessary) | Use Markdown equivalents — see "Angle-bracket placeholders" below |
| **MD034** | No bare URLs | Use `[text](url)` or autolink `<https://…>` |
| **MD037** | No spaces inside emphasis markers | ✅ `**bold**`  ❌ `** bold **` |
| **MD038** | No spaces inside code span markers | ✅ `` `code` ``  ❌ `` ` code ` `` |
| **MD039** | No spaces inside link text | ✅ `[link]`  ❌ `[ link ]` |
| **MD040** | Fenced code blocks must have a language | ✅ ` ```bash `  ❌ ` ``` ` |
| **MD047** | Files end with a single trailing newline | Always end with one `\n` |

`MD013` (line length), `MD041` (first-heading-must-be-h1), and `MD060` (table column style) are **disabled** in this repo's `.markdownlint.yaml` — line length is impractical for documentation with URLs, the H1 rule conflicts with files that start with an SPDX comment, and the project prefers aligned tables over a forced consistent column style.

## Angle-bracket placeholders

`<placeholder>` patterns are interpreted as raw HTML by markdownlint and will fail MD033. Two safe ways to write placeholders:

```text
✅ `<placeholder>`            (wrap in backticks → code span, fine)
✅ {placeholder}             (curly braces, no HTML interpretation)
❌ <placeholder>             (raw, MD033 will complain)
```

Inside fenced code blocks, raw `<placeholder>` is fine — the lint only fires on prose-level inline HTML.

## Tables

| Column 1 | Column 2 | Column 3 |
|---|---|---|
| Data 1 | Data 2 | Data 3 |

- Add a space after the opening `|` and before the closing `|`.
- Align columns for readability when practical (the project prefers aligned over forced-collapsed; `MD060` is disabled).

## Code-block language identifiers

Always specify the language. Common identifiers:

| Language | Identifier |
|---|---|
| Bash / Shell | `bash` or `shell` |
| JavaScript | `javascript` or `js` |
| TypeScript | `typescript` or `ts` |
| Python | `python` |
| JSON | `json` |
| YAML | `yaml` |
| Plain text | `text` |
| Markdown | `markdown` or `md` |
| Console output | `console` or `text` |

Use `text` for placeholders, command output, or anything that doesn't have a meaningful syntax.

## Nested code blocks

For nested code blocks, increase the number of backticks at each level. To embed a Python sample inside a Markdown sample, use four backticks for the outer fence and three for the inner:

`````text
````markdown
```python
print("hello")
```
````
`````

- Innermost: ≥3 backticks.
- Each outer level: at least one more backtick than the level inside it.
- Backticks per level must match between opening and closing fences.
- The outermost fence in *this guide* uses five backticks because the example itself contains a four-backtick fence; the same counting rule applies recursively.

## File hygiene

- One trailing newline at end of file (MD047). Editors usually add this; verify if you've hand-edited.
- No trailing whitespace on any line (MD009).
- No tabs for indentation (MD010) — use spaces.
- Maximum one blank line between blocks (MD012).
