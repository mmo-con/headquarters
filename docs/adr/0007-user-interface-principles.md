# ADR 0007: User interface principles

Status: Accepted, amended by [ADR 0013](0013-calmer-table-and-detail-panel.md)
and [ADR 0017](0017-detail-panel-order-and-commit-lines.md)

## Context

A dense repository overview must remain readable, comparable, and usable
without color or a particular font. Concrete keys, symbols, and filters are
specified in [the key binding reference](../keybindings.md).

## Decision

Color is never the only carrier of information. Every state has a symbol
with a count or text in each icon set; the help view has a legend.
`NO_COLOR` is honored. Known zero is `–`, unknown is `?`, and an
inapplicable count is empty.

Use lazygit-style panels, rounded borders and titles, with dialogs centered
over a dimmed main view. The footer shows only actions applicable to the
current mode, row, and selection; `?` shows all keys.

Put a health symbol in a separate left column, ordered by priority: errors,
critical states, dirty, behind, ahead, and other attention. Only healthy
names are not emphasized; [ADR 0013](0013-calmer-table-and-detail-panel.md)
limits emphasis to errors and critical states. Family rows summarize repository health; their
behavior in the tree is defined by
[ADR 0011](0011-families-in-the-tree.md), their layout and when they show a
summary by [ADR 0013](0013-calmer-table-and-detail-panel.md).

Each count has its own column: staged, modified, untracked, stashed, ahead,
and behind. Local work comes first, relation to upstream last. Findings
without counts share a textual State column. Apply colors per cell; success
has its own color, distinct from ahead.

Fixed-color themes that come in a dark and a light variant can be chosen as
a family. The variant follows the terminal background as Lip Gloss
determines it once at startup, before Bubble Tea owns the input; the result
is kept for the whole run, including reloads. The background is not queried
again while the interface runs, because the answer would interleave with
key input. An unknown background counts as dark.

## Consequences

- Table and detail panels sit side by side from 130 columns, otherwise
  stacked. Beside the table, the detail panel takes 40% of the width and at
  most 70 columns; when the table needs less, the detail panel moves left
  and takes the remaining width. Row highlighting ends at the table edge.
- The header has its own colors. Counters with matches are boxes in their
  state color; the active quick filter is bracketed even without color.
  [ADR 0013](0013-calmer-table-and-detail-panel.md) makes counters colored
  text and gives only the active quick filter a background.
- The detail panel puts the status of branch and worktree and errors first;
  its sections are defined by
  [ADR 0013](0013-calmer-table-and-detail-panel.md). It shortens the
  other-branches list before dropping section titles. Sections use titled
  rules; recent commits are one line each and oldest ones are dropped first.
- Commit times have the same absolute format in table and detail panel
  (`Nov 13 22:16`, with the year for earlier years). Narrow columns drop
  time of day and then day rather than truncating. Fetch and branch ages
  remain relative. [ADR 0013](0013-calmer-table-and-detail-panel.md)
  replaces the table's commit time with a relative age; the detail panel
  keeps the absolute time. [ADR 0017](0017-detail-panel-order-and-commit-lines.md) uses the age in the detail panel too.
- Column widths use all visible rows, avoiding shifts while scrolling. State
  reserves room for transient states and shrinks last. Count titles become
  symbols if full titles do not fit.
- The operations view groups by status and keeps the first visible group's
  title visible while scrolling. Unavailable entries keep their sort
  position and display their state.
