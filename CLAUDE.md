# go-share

Standard library only. Plan history: `docs/plans/`.

Invariants (keep when changing code):
- Delete a share's dir before its record (Sweep, API delete): a crash must leave a dangling record, never an orphaned public dir.
- Store mutations clone the slice, save `shares.json`, then swap in memory, so memory is never ahead of disk.
- Public responses always carry `Cache-Control: private, no-store`, including 404s. It is set in a ResponseWriter wrapper because `http.FileServer` strips it from error responses.
- The public file system hides directories without `index.html`: no listing anywhere.
- Extraction writes only regular files/dirs with fixed modes 0644/0755 and enforces the 100MB uncompressed cap while reading.
- The client validates a folder (has `index.html`) before opening any connection.
