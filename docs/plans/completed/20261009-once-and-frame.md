# One-time shares (`--once`) and the framed share page

## Overview
- `sharefly serve x --once`: the share can be opened once. The link waits (up to its TTL) until someone opens it; that visitor gets one minute to load the page and its assets, then it is gone for everyone.
- Every share link now opens a sharefly page (`/<id>`): a header with the expiry, the share in a full-size `<iframe>` (`/<id>/<entry>`, served unchanged), and a centered footer "Shared with sharefly" linking to the GitHub repo.

## Decisions
- **Frame, not rewrite**: the shared files are never modified; every file type gets the header/footer. Accepted costs: links inside the share to sites that forbid framing fail in the frame, the tab shows the share name, the address bar doesn't follow in-frame navigation. Old links `/<id>/<entry>` keep working without the frame (except for `--once` shares).
- **Click to open**: GET `/<id>` of an unopened once share shows an "Open" button; only its POST opens it, because chat apps fetch pasted links for previews.
- **Opening** atomically (store lock) sets a random open token and `expires_at = min(expires_at, now+1m)`, then sets cookie `sharefly_open_<id>` (`Path=/<id>`, HttpOnly, SameSite=Lax, Secure like the password cookie) and redirects to GET `/<id>`. A second POST gets 404.
- **Once shares need the open cookie** for the frame page and every file; unopened or without it: 404. Forwarded links never work.
- **Expired shares 404 at once** in the files handler, instead of staying public until the next 5-minute sweep.
- **Password cookie path** becomes `/<id>` (was `/<id>/`) so it also covers the frame page; still never matches `/<id>x`. Existing cookies keep working for files, visitors re-enter the password once for the frame.
- A password-protected once share shows the password form, then the Open button; the password POST never opens it.
- The open token is never in API responses. `once` is.
- Client: `--once` sends `once=1`; a server that ignores it (no `once` in the response) gets its share deleted and an error, like `--password`.

## Tasks
- [x] share: `Once`, `OpenToken` fields; `Store.Open`; tests
- [x] server: upload `once=1`; link URL `/<id>`; token hidden; tests
- [x] server: files handler — expiry 404, frame page, once page/open/cookie gate, password cookie path; tests
- [x] client + CLI: `--once`, old-server check, `ls` marks once; tests
- [x] dashboard: "once" badge
- [x] README, AGENTS.md invariants
