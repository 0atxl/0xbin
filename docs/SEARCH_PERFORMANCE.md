# Viewer Search Performance

**Status:** Phase 5 improvement

## Scope

Improve in-page search for large plaintext and decrypted pastes without changing
viewer security semantics, permanent wrapping, CodeMirror virtualization, or
the keyboard and visible search controls.

## Requirements

- A query scans the paste once and retains its match ranges for navigation.
- Moving to the next or previous match must not rescan the paste; the viewer
  reuses the cached ranges and updates only the selected-match decoration.
- Every occurrence is tinted, with a stronger treatment for the selected
  occurrence.
- The active match remains visible and is scrolled into view.
- Search counts remain readable and must not overlap navigation controls.
- Search continues to be case-insensitive and uses non-overlapping matches.

## Acceptance Criteria

- A 1 MiB-scale unit fixture validates dense match-range discovery and its
  exact count. Existing browser coverage verifies keyboard search and
  next/previous navigation.
- The search-count layout remains usable for four-digit counts on desktop and
  mobile viewports.
- Existing keyboard search, accessibility, wrapping, and hostile-content tests
  continue to pass.
