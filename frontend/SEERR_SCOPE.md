# Seerr scope-fence — what this frontend adopts vs. what it deliberately does NOT

This is the binding scope-fence artifact required by the redesign plan
(`.omc/plans/frontend-redesign-seerr.md`, Guardrail #3). The frontend is
**visually** inspired by [seerr-team/seerr](https://github.com/seerr-team/seerr)
(the Overseerr/Jellyseerr successor) but **must not** absorb Seerr's
multi-user request/approval model. sakms is single-operator and has no
permissions system — see the project `CLAUDE.md` ("Automation: manual by
default", "Staged-for-approval", "Single-operator auth"). Rename/Dedup/Purge
do carry one bounded bulk-apply exception since 2026-07-17 (an opt-in,
same-screen multi-select of already-reviewed Pending proposals — see
CLAUDE.md's amended engineering-convention note); it is unrelated to Seerr's
multi-user bulk request model and doesn't change anything below.

This list is validated against what actually shipped in Stage 1 Wave 3 (the
read-only Discover view). Each later wave that ports a mutating workflow must
re-check its own additions against the "NOT adopted" column before landing.

## Adopted from Seerr (visual / UX patterns only)

| Pattern | Where it lives now | Note |
|---|---|---|
| Discover-first landing (browse before search) | `src/screens/Discover.tsx` | The authed shell's default route is Discover, not a dashboard. |
| Hero banner + horizontal category rows (Netflix-style), not a flat grid | `Discover.tsx` `Hero` + `Row` | Movies/Series render a hero (top trending title) over scrollable Trending/Popular rows. |
| Poster cards with cover art | `Discover.tsx` `PosterCard` | 2:3 poster tiles; graceful text-tile fallback when art is missing. |
| Availability badges on cards | `Discover.tsx` `AvailabilityBadge` | "N available" / "no release" — a read-only indexer probe, not a request action. |
| Dark-theme-primary palette | `src/index.css` (`@theme` tokens) | Carried over from the original vanilla-JS frontend; continuous look. |
| Scene/thumbnail art for the adult catalog | `Discover.tsx` `AdultCard` | Scene-shaped (TPDB), not title-shaped; art via the image proxy where TPDB provides it. |
| All poster/thumbnail art proxied through the backend | `src/api/discover.ts` `proxyImage` / image proxy | Adopts the *look* of rich art without leaking operator browsing to TMDB/TPDB (plan Decision #7). |

## NOT adopted (explicitly out of scope — would violate sakms's model)

| Seerr pattern | Why it is rejected |
|---|---|
| **Multi-user request queue** (users submit, an admin approves) | sakms is single-operator; there is no "requester" vs. "approver" split, no request inbox, no pending-approval list. |
| **User accounts, roles, per-user permissions** | One login gates the whole app across all three auth modes; no user table, no roles surface. Adding any per-user UI is out of scope. |
| **Bulk request / "request all seasons" / multi-select grab** | Discover/grab stays strictly one title/scene per user action — no "request all seasons," no multi-select grab, no batch request queue. (Rename/Dedup/Purge's 2026-07-17 bulk-apply is a different thing: an opt-in, same-screen multi-select of proposals the operator already individually reviewed at Scan time, not a Seerr-style bulk *request*. See CLAUDE.md's amended note.) |
| **Approval / decline queues, request-status dashboards** | No request lifecycle exists to track; nothing is queued for someone else to action. |
| **Issue reporting / comments / notifications-to-requesters** | Social/multi-user collaboration features with no place in a single-operator tool. |
| **"Requested by" / user avatars / watchlist-per-user** | Identity-scoped UI; sakms has exactly one operator identity. |
| **Auto-approve rules keyed on user/role/quota** | No users, no quotas, no roles to key on. (sakms auto-grab, Stage 2, is keyed on release *quality* floors, not on *who* asked.) |

## Validated against Stage 1 Wave 3 (read-only Discover)

Confirmed in the shipped view and its tests (`src/screens/Discover.test.tsx`):

- Clicking a poster/scene card **mutates nothing** — cards show detail
  (title/year/rating/overview tooltip) and an availability badge only. There
  is **no grab/request/approve button** on any card in this wave.
- No multi-select, no "select all", no batch bar, no request queue UI exists
  in the Discover surface.
- Availability is a **read** ("does a release exist?"), never a write.
- Every image `<img src>` is a same-origin `/api/images/proxy?url=...`
  request — verified by both a component test and a live browser
  network-capture run (zero direct `image.tmdb.org` / TPDB requests).
