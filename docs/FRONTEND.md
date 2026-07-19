# 0xbin Frontend Design and Implementation Plan

**Status:** Implemented self-host direction for Steps 11–16  
**Current release note:** The quiet one-hour viewer lifetime treatment remains
an outstanding Phase 3 item.  
**Sources of truth:** `../spec.md`, `PRD.md`, `TECHNICAL_DESIGN.md`, `IMPLEMENTATION_PLAN.md`, and `../AGENTS.md`

This document settles the visual and interaction direction for the 0xbin MVP
and breaks Step 11 into reviewable implementation slices. Product and security
semantics from `spec.md` still win if this document is interpreted ambiguously.

## 1. Settled Product Decisions

- The editor is the creation page, not a card inside a dashboard.
- The interface is borderless, quiet, and developer-focused, with a restrained
  terminal character rather than a conventional admin-dashboard appearance.
- The MVP has four user-facing lifetime choices:
  - **View once**
  - **1 hour**
  - **1 day**
  - **3 days**
- View once maps to `burn_after_read: true`. It also receives a bounded server
  expiry so an unopened paste is not retained indefinitely. Use `72h` as that
  safety expiry. The remaining choices map to `burn_after_read: false` and
  `1h`, `24h`, or `72h` respectively.
- `1 day` is the default lifetime.
- Encryption is optional and off by default.
- After successful creation, the browser builds the final sharing URL, copies
  it, and navigates directly to the viewer.
- An encrypted sharing URL includes its key only in the URL fragment. The full
  URL, including `#key`, is copied. The server must never receive the key.
- The viewer includes a **Create new paste** action.
- Viewer content wraps long lines permanently. The MVP has no horizontal
  scrolling or wrap/no-wrap toggle.
- Copy success is shown through a small accessible toast.
- The encrypted key gate appears only when an encrypted paste is opened without
  a usable fragment key.
- There is no creator/author field in the MVP. The payload remains title,
  language, and content.
- Missing, expired, deleted, and consumed pastes use the same public unavailable
  state.
- Visual styling must not weaken keyboard access, focus visibility, contrast,
  reduced-motion support, or safe text rendering.

## 2. Codex Change Request — Step 10A: Three-Day Expiry

This small backend change precedes frontend lifetime controls.

### Required behaviour

- Accept the stable expiry identifier `72h` in addition to `1h` and `24h`.
- Keep `24h` as the default expiry.
- Change the maximum allowed configured expiry from 24 hours to 72 hours.
- Keep durations longer than 72 hours invalid.
- Add `72h` to the default `OXBIN_ALLOWED_EXPIRIES` value.
- Do not change the database schema; expiry timestamps already support this.
- Keep View once implemented through the existing `burn_after_read` flag and
  atomic consume flow.

### Expected code and documentation updates

- `internal/config/config.go`
  - Default allowed expiries: `1h,24h,72h`.
  - Validation upper bound: 72 hours.
- `internal/config/config_test.go`
  - Verify the new defaults and 72-hour boundary.
  - Verify values above 72 hours remain invalid.
- `internal/paste/expiry.go`
  - Add `72h` to `DefaultExpiryPolicy`.
  - Raise the policy upper bound to 72 hours.
- `internal/paste/expiry_test.go`
  - Verify exact 72-hour calculation.
  - Verify a longer duration is rejected.
- `docs/openapi.yaml`
  - Add `72h` to the create-request expiry enum.
  - Update the API description if its implementation-step status is stale.
- `spec.md`, `docs/PRD.md`, `docs/TECHNICAL_DESIGN.md`, and `docs/PHASES.md`
  - Record three-day expiry as an MVP choice rather than a post-MVP candidate.
- `README.md`
  - Keep implementation status aligned with completed steps.

### Verification gate

- `1h`, `24h`, and `72h` create requests succeed.
- A `72h` expiry is calculated from server time exactly.
- Durations above `72h` fail configuration and policy validation.
- View once remains non-consuming on GET and atomic on explicit consume.
- `make format`, `make lint`, `make test`, `make test-race`, `make test-e2e`,
  and `make build` pass.

## 3. Information Architecture

### Routes

- `/` — creation experience
- `/{slug}` — paste viewer, encrypted key gate, or View-once confirmation

Do not create a separate `/error` route. A failed creation remains on `/`, and
a failed retrieval retains the requested paste URL. Keeping the original URL
allows a retry and avoids throwing away a possibly valid encrypted link.

### Primary frontend states

Creation:

- empty
- editing
- validating
- encrypting
- submitting
- success/navigation
- failure

Viewer:

- loading
- plaintext ready
- encrypted key required
- decrypting
- decrypted ready
- View-once confirmation
- consuming
- unavailable
- service error

Keep state local to the relevant screen. Do not add a global state library
unless a demonstrated problem requires it.

## 4. Visual System

### Brand and logo treatment

Use one compact, path-based bin/X brand icon. Do not render `0xbin` as header
text.

- The icon uses the accent colour and is also the new-paste action.
- Use the same mark as the favicon where practical.
- Do not add a mascot, gradient emblem, or large logo animation for the MVP.
- The default mark remains static.

### Colour tokens

Use semantic CSS custom properties rather than component-specific colour
values. These are the initial tokens; adjust only when contrast testing shows a
problem.

#### Dark theme

```css
--background: #181719;
--surface: #211f22;
--surface-raised: #29262a;
--text-primary: #f4f0ed;
--text-muted: #aaa2a7;
--border-subtle: rgba(244, 240, 237, 0.12);
--gutter: rgba(244, 240, 237, 0.38);
--accent: #e0ccd0;
--accent-soft: rgba(224, 204, 208, 0.14);
--accent-contrast: #2a1920;
--danger: #d58c99;
```

#### Light theme

```css
--background: #fcf8f1;
--surface: #f3ece2;
--surface-raised: #fffdf8;
--text-primary: #2d2723;
--text-muted: #7b716a;
--border-subtle: #e4dacf;
--gutter: #aaa097;
--accent: #74172f;
--accent-soft: rgba(116, 23, 47, 0.1);
--accent-contrast: #fff8f9;
--danger: #9b2540;
```

Dark mode uses charcoal rather than blue-black. Light mode uses warm off-white
rather than pure white. The wine/mauve accents are used sparingly for the brand
icon, active encryption state, focus details, and brief success feedback.

### Typography

- UI: Geist or a similar neutral sans-serif, with a system-sans fallback.
- Editor/viewer: Geist Mono, IBM Plex Mono, or JetBrains Mono, with a system
  monospace fallback.
- Do not load executable font scripts or third-party runtime assets.
- Keep the type scale compact and functional. The paste content remains the
  strongest visual element.

### Shape, spacing, and borders

- Avoid dashboard cards around the editor and viewer.
- Use hairline borders only for focus, separation, and floating surfaces.
- Controls may use small radii; avoid heavily rounded pill styling everywhere.
- Maintain generous canvas space and compact metadata/actions.
- The line-number gutter may be translucent or lightly frosted. Do not blur the
  numbers themselves.

### Motion

- Standard control transitions: 120–180 ms. Theme changes use a gentler
  450 ms colour transition so the full-canvas palette does not switch
  abruptly.
- Use motion for theme changes, menu expansion, key-gate fade, copy feedback,
  and control-state changes.
- Do not use page-scale entrance animations.
- The **Reveal and destroy** transition may disperse the confirmation surface
  like fine sand moving into wind. Complete the destructive dissolve only after
  the consume request succeeds.
- Under `prefers-reduced-motion: reduce`, replace sand dispersion with a short
  opacity change and no particle movement.

## 5. Shared Shell

### Desktop arrangement

- Brand icon: top-left
- Icon-only theme toggle: top-right; its accessible name describes the action
- Main page content: fixed to the viewport between the top controls and bottom
  actions; the document itself does not scroll
- Editor/viewer canvas: the only primary scroll region
### Self-host navigation

The self-hosted distribution intentionally omits the corner menu and its
marketing, legal, and policy destinations. Those hosted-public destinations
belong to Step 17 and may be added in the later public fork. Focused encrypted
key and View-once gates omit the visible logo and theme toggle.

### Theme behaviour

- Respect `prefers-color-scheme` on first use.
- Permit manual light/dark selection.
- Persist only the theme preference. Never persist paste content or encryption
  keys.

## 6. Creation Experience

### Layout

- A compact metadata row near the top contains:
  - Title (optional)
  - Language selector (optional; `plaintext` fallback)
- The CodeMirror 6 editor fills nearly all remaining space.
- A muted line-number gutter remains visible without dominating the canvas and
  shares the exact canvas background; no editor box or contrasting gutter
  panel is permitted.
- A compact bottom action region contains lifetime, encryption, size, and the
  primary action.
- Bottom-right control order is size, lifetime, encryption, then Create.
- Language and other dropdown controls use application-styled popovers rather
  than the platform-native select menu. Popovers match their trigger width and
  do not use decorative shadows.
- There is no Creator field.

### Lifetime control

Present one clear selection:

```text
View once    1 hour    1 day    3 days
```

- Default: `1 day`.
- Selecting View once shows a brief accessible toast:
  `Destroyed after one deliberate reveal. If unopened, it expires after 3 days.`
- Mapping:

| UI choice | API expiry | `burn_after_read` |
|---|---:|---:|
| View once | `72h` | `true` |
| 1 hour | `1h` | `false` |
| 1 day | `24h` | `false` |
| 3 days | `72h` | `false` |

### Encryption control

Place the encryption toggle close to the Create action.

Default copy:

```text
Encrypt this paste
```

When enabled, show a brief accessible toast:

```text
The key stays in the copied link and is never sent to 0xbin.
```

Title, language, and content are encrypted together. No metadata from an
encrypted payload is submitted separately as plaintext.

### Editor behaviour

- Content is required.
- Show a byte-aware size counter against the configured 1 MiB limit.
- Give clear feedback before the limit is exceeded and prevent invalid submit.
- `Ctrl/Cmd + Enter` creates the paste.
- With an empty selection, Tab inserts a tab at the current cursor position;
  Shift-Tab outdents. A multi-line selection may still be indented as a block.
- Provide CodeMirror undo and redo with standard platform shortcuts. Consecutive
  letters in a word are grouped into one undo step; whitespace starts the next
  word's step. Auto-closed bracket pairs undo together, and a following
  delete/backspace undoes before the preceding typing.
- Language selection is explicit; automatic detection is out of MVP scope.
- Selected supported languages enable CodeMirror syntax highlighting,
  language-aware eight-column indentation, and automatic bracket/quote
  pairing. Initial support includes plain text, JavaScript, TypeScript, HTML,
  Python, Go, C, C++, Java, and Rust. Other values use the plain-text fallback.
- Syntax highlighting uses semantic colours with separate light and dark
  palettes; the dark palette prioritizes readable, high-contrast tokens rather
  than reusing the light palette unchanged.
- Keep the language popover height bounded and scroll its options rather than
  allowing it to displace or escape the editor viewport.
- Leave a narrow gap between language-option highlight surfaces so hovering an
  adjacent option never visually joins the selected option.
- Close the language popover when a click moves to the editor or any other
  control outside the selector.
- Keep the language popover open after a language selection so another option
  can be chosen without reopening it.
- Load syntax parsers on selection rather than placing every supported language
  in the initial browser bundle.
- Use a plain text area fallback if CodeMirror cannot initialize.

### Submission sequence

1. Validate content, metadata byte limits, and lifetime.
2. Disable duplicate submission.
3. If encryption is enabled, encrypt the versioned payload in the browser.
4. Send plaintext payload or ciphertext envelope to the create API.
5. Receive the canonical clean URL and expiry.
6. For encrypted content, append the key fragment locally.
7. Copy the complete final sharing URL.
8. Navigate to the viewer using that same URL.
9. Show a small toast: `Link copied`.

If clipboard access fails, still navigate and show:
`Paste created — copy the link manually.` The viewer's Copy link action must
remain available.

## 7. Viewer Experience

### Layout

- Wordmark top-left and theme control top-right.
- Optional title above the content.
- Do not repeat the selected language name in the viewer toolbar.
- Compact actions aligned to the upper-right of the content:
  - Copy
  - Raw or Download
  - Search
  - Create new paste
- Borderless content canvas with line numbers when useful.
- Omit empty metadata rather than displaying placeholder values.

### Plaintext viewer

- Render content as untrusted text or trusted syntax tokens, never HTML.
- Copy places the exact paste content on the clipboard.
- Raw opens the safe server raw endpoint for active non-burn plaintext pastes.
- Download uses a safe filename derived without trusting arbitrary title input.
- Search works through keyboard and visible controls.
- Long lines always wrap in the viewer and editor.

### Encrypted viewer

- When a fragment key is present, fetch the envelope and decrypt locally.
- Never include the fragment in an API request, analytics event, error report,
  or log.
- Raw/Download is created locally from the decrypted content using a browser
  Blob. Do not request plaintext from the server raw endpoint.
- After successful decryption, remove the gate and show the normal viewer.

### Lifetime display

Show the selected lifetime as quiet supporting information rather than a large
badge. View-once pastes must make their destructive behaviour prominent before
reveal. Timed pastes may display a human-readable relative expiry without
running a distracting second-by-second countdown.

## 8. Encrypted Key Gate

Show this focused gate only when the retrieved paste is encrypted and the URL
does not contain a valid fragment key.

```text
Encrypted paste

[ Paste decryption key                         ] [Enter]

The key is processed only in this browser.
```

- Accept a raw Base64url key, a `#key` value, or a complete encrypted URL.
- Enter submits; Escape closes only when returning to a safe page is possible.
- Trap focus correctly while the gate is active.
- Show `Decrypting…` in the submission control.
- Use one generic inline failure: `Unable to decrypt — check the key.`
- Never persist the key outside the fragment/current page memory.
- The background is an abstract muted canvas, never a blurred preview of the
  protected paste.

## 9. View-Once Gate

Opening a View-once URL must not retrieve or consume the content. The initial
GET returns confirmation metadata only.

```text
View-once paste

Opening this paste will permanently destroy the server copy.
It cannot be opened again.

[ Reveal and destroy ]
```

For an encrypted View-once paste:

- Obtain the key before enabling Reveal.
- Validate only key format before consume.
- Show this warning:
  `0xbin cannot verify this key before the paste is consumed.`
- The user must make one deliberate confirmation.
- The consume POST atomically retrieves and deletes the envelope.
- Attempt local decryption only after successful consume.
- A correctly formatted but wrong key can permanently consume content without
  revealing it; the warning must not imply otherwise.

The sand-dispersal transition begins as restrained feedback when Reveal is
activated and completes only after the server confirms successful consumption.
Do not let animation delay access to successfully returned content.

## 10. Feedback and Error Behaviour

### Toasts

Use compact toasts for transient results:

- `Link copied`
- `Content copied`
- `Paste created — copy the link manually`
- `Could not create paste — try again`
- `Too many requests — try again in {duration}`
- temporary network recovery feedback

Toasts appear beside the top-right theme control in compact accent-tinted
boxes. They queue visually: the first is on top and later messages stack below
it. Each has a standard six-second timeout. Hovering or keyboard-focusing any
toast pauses the timers for the entire stack. Toasts use a consistent minimum
height while allowing longer errors and warnings to wrap naturally. A thin
bottom-edge time bar shows each remaining lifetime, and an explicit close
button dismisses it immediately. Toasts must use an ARIA live region, remain
readable long enough, and not rely only on colour or animation.

### Persistent states

Do not use a disappearing toast as the only content when the viewer cannot be
shown.

Unavailable state:

```text
Paste unavailable

This paste may have expired, been consumed,
been deleted, or never existed.

[ Create a new paste ]
```

Retain the original requested URL. Do not reveal which lifecycle event caused
the unavailable state.

Service failure:

```text
0xbin is temporarily unavailable

[ Try again ]    [ Create a new paste ]
```

Decryption failures remain inline within the key gate. Validation failures stay
next to their relevant control, with a summary only when needed for keyboard or
screen-reader users.

### Loading

- `Creating…` in the Create action.
- `Decrypting…` in the key gate.
- `Revealing…` in the View-once action.
- Subtle skeleton text lines for a normal viewer load.
- Avoid blocking spinners that cause layout shifts.

## 11. Component Direction

Use focused components rather than one large `App.tsx`.

```text
AppRouter
AppShell
BrandIcon
ThemeToggle
ToastRegion
CreatePage
MetadataFields
CanvasEditor
LifetimeSelector
EncryptionToggle
CreateAction
PastePage
PasteViewer
ViewerActions
KeyGate
BurnGate
UnavailableState
ServiceErrorState
```

Supporting modules:

```text
api/types
api/client
routes
crypto
clipboard
download
theme
formatting
```

Keep API response types centralized. Map API error codes to presentation copy
in one place rather than scattering string comparisons through components.

## 12. Responsive Behaviour

- Desktop retains the full-canvas layout and compact horizontal metadata row.
- On narrow screens, metadata and bottom controls stack without hiding labels.
- Viewer actions may collapse into an accessible action menu, but Copy and
  Create new paste remain easy to reach.
- The corner menu is tap- and keyboard-operable; never hover-only.
- Gates fit within the viewport with the on-screen keyboard open.
- Content remains selectable and horizontally scrollable when wrapping is off.

## 13. Accessibility Requirements

- Use semantic form labels and headings.
- Provide full keyboard operation and logical focus order.
- Make focus visible without adding heavy permanent borders.
- Return focus sensibly after closing menus and gates.
- Announce asynchronous success and failure through live regions.
- Do not communicate state through colour alone.
- Meet WCAG AA contrast for text, controls, and focus indicators.
- Respect reduced-motion and system theme preferences.
- Test keyboard-only creation, copying, key entry, search, and View-once reveal.

## 14. Security and Privacy Requirements

- Render paste content as hostile input.
- Never use arbitrary HTML injection for user content.
- Never persist paste content, decrypted metadata, or keys in local storage,
  session storage, IndexedDB, service-worker caches, or analytics.
- Strip/redact URL fragments from all diagnostics.
- Do not load third-party executable scripts.
- Do not send encrypted title or language separately from ciphertext.
- Do not call the server raw endpoint for encrypted or View-once content.
- Preserve generic missing/expired/deleted/consumed behaviour.
- Test script tags, event attributes, SVG payloads, Markdown HTML, bidi/control
  characters, malformed envelopes, wrong keys, and modified ciphertext.

## 15. Ordered Step 11 Implementation Slices

Each slice should be independently reviewable and finish with tests. Do not
pull Step 12 embedding/container work into Step 11.

### 11.1 Frontend foundation

- Add the typed API client and error mapping.
- Add the `/` and `/{slug}` route shell without a heavy routing dependency if a
  small history-based router is sufficient.
- Establish CSS tokens, theme initialization, typography fallbacks, and base
  reset.
- Add component and API-client test scaffolding.

**Gate:** Route resolution, theme selection, and representative API success and
error decoding pass unit tests.

### 11.2 Shared shell and feedback

- Implement brand icon, icon-only theme toggle, corner menu, focus treatment, and toast
  region.
- Implement responsive shell behaviour.
- Verify keyboard and screen-reader announcements.

**Gate:** Shell works in both themes, at narrow and wide widths, with keyboard
only and reduced motion.

### 11.3 Creation form and editor

- Implement metadata inputs, lifetime selector, encryption toggle, byte
  counter, validation, and CodeMirror editor with fallback.
- Add `Ctrl/Cmd + Enter` submission.
- Keep `1 day` as the default.

**Gate:** Required content, metadata limits, lifetime mapping, size limits,
duplicate-submit prevention, and keyboard behaviour pass tests.

### 11.4 Plaintext creation and navigation

- Connect the plaintext create request.
- Copy the returned URL, navigate to the viewer, and announce success.
- Handle clipboard failure, validation, payload-too-large, rate-limit, and
  temporary service errors.

**Gate:** A plaintext paste can be created from the UI and opens at its returned
slug; copy feedback is accessible.

### 11.5 Plaintext viewer

- Fetch and safely render plaintext pastes.
- Implement Copy, Raw, Download, Search, permanent wrapping, and Create new
  paste.
- Implement loading, unavailable, and service-error states.

**Gate:** Plaintext viewing works for large and malicious test content without
execution, and all viewer actions work with keyboard only.

### 11.6 Encrypted creation and viewing

- Integrate the existing Web Crypto module into creation.
- Append the key fragment locally and copy/navigate with the complete URL.
- Decrypt from a present fragment.
- Implement the missing-key gate accepting raw keys and complete URLs.
- Generate encrypted downloads locally.

**Gate:** Correct-key, missing-key, wrong-key, modified-ciphertext, Unicode, and
fragment-not-transmitted journeys pass.

### 11.7 View-once flow

- Render confirmation metadata without content.
- Implement deliberate atomic consume.
- Integrate encrypted View-once key collection and irreversible warning.
- Add sand-dispersal and reduced-motion transitions.

**Gate:** GET never consumes; one deliberate action consumes; later attempts are
generically unavailable; encrypted wrong-key risk is clearly presented.

### 11.8 Browser journeys and hardening

- Replace the end-to-end placeholder with real browser journeys.
- Cover all four lifetime choices.
- Cover plaintext, encrypted, missing-key, wrong-key, View-once, unavailable,
  rate-limit, service failure, clipboard failure, and responsive layouts.
- Run an automated accessibility baseline.
- Test at least 10,000 viewer lines and the supported maximum payload.
- Verify fragments never appear in request targets, bodies, headers, or logs.

**Gate:** The complete Step 11 verification gate in
`IMPLEMENTATION_PLAN.md` passes.

### 11.9 Documentation closeout

- Update `README.md` and `IMPLEMENTATION_PLAN.md` after each completed step.
- Record any deferred visual refinements without expanding MVP behaviour.

## 16. Step 11 Completion Checklist

- [x] Backend accepts `1h`, `24h`, and `72h`.
- [x] Lifetime selector exposes View once, 1 hour, 1 day, and 3 days.
- [x] Create page is a full-screen editor canvas.
- [x] Plaintext creation and viewer journeys pass.
- [x] Encrypted creation never transmits plaintext or key.
- [x] Missing-key and wrong-key journeys pass.
- [x] View-once GET never consumes content.
- [x] View-once deliberate consume works exactly once.
- [x] Copy, Raw/Download, Search, permanent wrapping, and Create new paste work.
- [x] Transient and persistent error states are implemented.
- [x] Malicious paste content cannot execute.
- [x] Keyboard-only and accessibility checks pass.
- [x] Reduced-motion alternatives work.
- [x] Large-content viewer remains usable.
- [x] All repository verification commands pass.

## 17. Explicitly Deferred

- Creator/author attribution
- Accounts or identity
- Permanent paste storage
- 7-day or longer expiry
- Automatic language detection
- File or image uploads
- Public galleries, comments, revisions, or forks
- Final marketing pages and elaborate brand animation
