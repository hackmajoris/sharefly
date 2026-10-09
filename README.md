# sharefly

Share a file or folder via a link, straight from your terminal.

```
$ sharefly serve report.html
started local sharefly server (pid 4242, log ~/.local/state/sharefly/server.log)
http://127.0.0.1:7788/k7f3x9qa2m
```

What visitors get depends on what you share:

| You share | The link opens |
|---|---|
| an HTML file, or a folder with `index.html` | the page itself |
| a Markdown file | the Markdown as a formatted page |
| any other file (zip, PDF, image, …) | a download page with the file's name, size and a Download button |

Every link shows the share's name and expiry in a bar above it.

Works locally with zero config. Add a Cloudflare Tunnel and the links work for anyone on the internet. Start it on one machine with Tailscale and you can share from all your devices.

- [Install](#install)
- [Cookbook](#cookbook): copy-paste recipes for common tasks
- [Reference](#reference): commands, environment, server flags, HTTP API
- [Good to know](#good-to-know)

## Install

```
brew install --cask hackmajoris/apps/sharefly
```

Upgrade with `brew upgrade --cask sharefly`. From source (Go 1.27.1+): `make build`, which puts the binary in `.bin/sharefly`.

## Cookbook

### Share a single page

```
sharefly serve report.html
```

The first `serve` starts a background server for you. The command prints only the link, so it's easy to script.

### Share any other file

```
sharefly serve backup.zip
```

A single file that isn't HTML or Markdown (an archive, a PDF, an image) gets a download page: its name, size and a Download button.

### Share a Markdown file

```
sharefly serve notes.md       # → http://127.0.0.1:7788/<id>
```

A single `.md` or `.markdown` file is shared as a formatted page: headings, lists and task lists, tables, code blocks, quotes, links and emphasis, in light or dark to match the reader's system. The page title is the first `# heading`, and a Download button at the top gives readers the original `.md` file (embedded in the page, so the share stays a single file). Raw HTML in the file shows as text, and links other than `http`, `https`, `mailto` and relative ones are dropped. Markdown files inside a shared folder are served as they are.

### Share a whole site

```
sharefly serve ./site
```

The folder needs an `index.html` at its root. Links in your HTML must be relative (`style.css`, not `/style.css`), because every share lives under its own `/<id>/` path.

### Pick how long a link lives

```
sharefly serve report.html --ttl 1h      # minutes: 30m, hours: 1h, days: 3d
sharefly serve report.html --ttl never   # until you delete it
```

The default is 7 days. Expired links stop working at once; their files are deleted within 5 minutes.

### Let a link be opened only once

```
sharefly serve secret.html --once
```

The link waits until someone opens it (up to its `--ttl`). Visitors first see an Open button, so chat apps that fetch links for previews don't use up the view. The visitor who presses it gets the page, and only that browser can load it, for one minute; after that, or for anyone else, the link is gone. Combine with `--password` to also ask for a password first.

### See, extend and delete links

```
sharefly ls                        # ID, name, expiry and URL of every share
sharefly renew k7f3x9qa2m --ttl 7d # expire 7 days from now
sharefly rm k7f3x9qa2m             # gone immediately
```

### Protect a share with a password

```
sharefly serve report.html --password
password: k7f3-x9qa-2mzt-c4wq (shown only now)
https://share.yourdomain.com/k7f3x9qa2m
```

The server generates the password and prints it once, on stderr, so `| pbcopy` still copies only the link. Visitors get a password page; after the right password, a cookie for that share keeps them in until the browser closes. Scripts can send it as HTTP Basic auth instead: `curl -u x:<password> <url>`. sharefly stores only a hash, so a lost password can't be shown again: `rm` the share and serve it again.

### Manage shares in your browser

```
sharefly dashboard            # opens it in your browser and prints the URL
```

It opens the page of the server your commands talk to (`server` setting or `--server`): `http://127.0.0.1:7787/` locally, or `http://<host-tailscale-name>:7787/` on a device pointed at another host. Like `serve`, it starts a local server first if none is running. The page lists every share with its link, expiry, size and whether it has a password or is one-time, and lets you copy a link, renew a share or delete it. Markdown shares also have a Download .md button.

The page is served only on `api-addr`, never on the public file server, so it is as private as the API: this machine, or your tailnet. It has no login of its own; anyone who can reach `api-addr` can use it, just like the CLI.

### Copy the link or open it right away

```
sharefly serve report.html | pbcopy          # link on the clipboard
open "$(sharefly serve report.html)"         # open it in your browser
```

### Make links public, instantly (quick tunnel)

No domain or Cloudflare account needed. sharefly runs `cloudflared` for you and uses the random address it gets, even if `public-url` is set in the config (only an explicit `--public-url` overrides it).

```
brew install cloudflared
sharefly config set tunnel quick
sharefly serve report.html     # → https://<random-words>.trycloudflare.com/<id>
```

For one run without changing the config: `sharefly serve report.html --tunnel quick`. The flag only applies when `serve` starts the server; if one is already running in another mode, `serve` refuses and tells you to `sharefly stop` first. The server keeps the tunnel until `sharefly stop`, so a later plain `serve` still gets a public link.

The address changes every time the server starts, so old links stop working after `sharefly stop`. Use your own domain for links that last.

### Make links public on your own domain

You need a domain on Cloudflare.

1. In the Cloudflare dashboard, go to Zero Trust → Networks → Tunnels and create a tunnel. On the install screen copy only the token (the long `eyJ…` string); don't run the install command. Add a public hostname, e.g. `share.yourdomain.com`, with service `HTTP` and URL `127.0.0.1:7788`. Leave Path empty.
2. Give sharefly the token and your domain. The token is a secret: it's stored in the config file, which only you can read, and is never shown on the command line.
   ```
   brew install cloudflared
   sharefly config set tunnel-token eyJ...
   sharefly config set public-url share.yourdomain.com
   sharefly config set tunnel token
   sharefly serve report.html     # → https://share.yourdomain.com/<id>
   ```

Token mode needs both the token and `public-url` (or `--public-url`); without either, the server refuses to start and `serve` says why before starting anything.

sharefly starts `cloudflared` together with its server, restarts it if it crashes, and stops it with `sharefly stop`; its output goes to the server log. If you installed cloudflared as a system service earlier, remove it (`sudo cloudflared service uninstall`) so two connectors don't run side by side.

### Share from all your devices

Pick one machine to host the shares (it needs the Cloudflare Tunnel from the previous recipe), and use Tailscale to reach it.

1. On the host, make the server listen on its tailnet address:
   ```
   sharefly config set api-addr "$(tailscale ip -4):7787"
   sharefly start
   ```
   The host's own `serve`, `ls`, `rm` and `renew` follow `api-addr` automatically, and `serve` still auto-starts the server there.
2. On every other device, install sharefly and point it at the host:
   ```
   sharefly config set server http://<host-tailscale-name-or-ip>:7787
   ```
3. `sharefly serve`, `ls`, `rm` and `renew` now work against the host. A remote server is never auto-started.

The server runs until you stop it, the host sleeps or it reboots. After that, run `sharefly start` again (or just `sharefly serve` on the host). Shares on disk are kept and served again as soon as it starts.

### Start the server at boot

```
sharefly stop                 # if one is running
sharefly service install      # asks for sudo once
```

This installs a launchd daemon on macOS (`/Library/LaunchDaemons/dev.hackmajoris.sharefly.plist`) or a systemd unit on Linux (`/etc/systemd/system/sharefly.service`). It runs `sharefly server` as you, at boot and without a login, restarts it within 10 seconds if it exits, and keeps restarting until it can bind (for example until Tailscale is up). It reads your config file like any other server and logs to `<data-dir>/server.log`; `cloudflared` runs as its child.

With the service installed, `sharefly stop` and `sharefly start` stop and start the service (both ask for sudo), `sharefly config set` restarts it, and `serve` never starts a second server next to it. `sharefly service status` shows its state; `sharefly service uninstall` removes it. After `brew upgrade`, run `sharefly stop && sharefly start` to pick up the new binary.

### Start and stop the server yourself

```
sharefly start                                        # background; returns once it answers
sharefly start --public-url https://share.example.com # with flags, e.g. a public URL
sharefly stop
```

You rarely need `start`: `serve` starts the server when none is running. Use `sharefly server` instead to keep it in the foreground and watch its log.

### Troubleshooting

| Symptom | What to do |
|---|---|
| `can't reach sharefly server at ...` | For a remote server: check `tailscale status` and that the server is running. The `server` setting must include `http://` (`sharefly config` shows it). |
| `local server did not come up` | Read the log: `tail ~/.local/state/sharefly/server.log`. Usually ports 7787 or 7788 are taken: `lsof -iTCP:7787 -iTCP:7788 -sTCP:LISTEN`. |
| `a sharefly server is answering ... but has no pid file` | A server started by an older version or with another `--data-dir` is still running. Stop it with the `kill` command from the message. |
| Public link shows Cloudflare 502 | cloudflared can't reach the file server. The sharefly server must be running, and the tunnel's hostname must point at `127.0.0.1:7788`. |
| Public link shows Cloudflare 1033 | The tunnel is down. Look for `cloudflared:` lines in `~/.local/state/sharefly/server.log`; a wrong token shows up there. Check `sharefly config` for `tunnel` and `tunnel-token`. |
| Public link gives a DNS error | The domain isn't Active on Cloudflare yet, or the tunnel has no public hostname. |
| `tunnel is ... but cloudflared isn't installed` | `brew install cloudflared`. |
| Public link shows `404 page not found` | The share expired or was deleted. Check `sharefly ls`. |
| Links still show `127.0.0.1` | Check `sharefly config`. If the server was started with `--public-url`, that flag overrides the config file. |

## Reference

### Commands

```
sharefly serve <file|folder> [--ttl 7d] [--server URL] [--tunnel M] [--password] [--once]   # upload, print only the URL
sharefly ls [--server URL]                                                         # table: ID NAME EXPIRES URL
sharefly dashboard [--server URL]                                                  # open the management page in your browser
sharefly rm <id> [--server URL]                                                    # delete a share, prints nothing
sharefly rm --all [--server URL]                                                   # delete every share, prints nothing
sharefly renew <id> [--ttl 7d] [--server URL]                                      # reset expiry from now, prints the row
sharefly start [flags]                                                             # start the server in the background
sharefly server [flags]                                                            # run the server in the foreground
sharefly stop [--data-dir DIR]                                                     # stop the local server
sharefly service install | uninstall | status                                      # run the server at boot (launchd / systemd)
sharefly config                                                                    # show settings and where they come from
sharefly config set <key> <value>                                                  # save a setting
sharefly config unset <key>                                                        # remove a setting
sharefly config open                                                               # edit the config file in your editor
```

- `--password` (serve): protect the share with a generated password, printed once on stderr.
- `--once` (serve): one visitor can open the share; it expires a minute after they do.
- `--tunnel` (serve): tunnel for the server `serve` starts (`off`, `quick`, `token`). Error if a server already runs in another mode, or if `--server` is another machine.
- `--ttl`: `Nm` (minutes), `Nh` (hours), `Nd` (days), N a positive integer, or `never`. Default `7d`.
- Flags may come before or after the positional argument.
- A share's URL is `/<id>`: a page with the share's name and expiry on top, the share itself in a frame (served unchanged at `/<id>/<entry>`) or, for a single file that isn't `.html`/`.htm` (Markdown is stored as `.html`), a Download button, and a "Shared with sharefly" footer. Links inside the share to sites that refuse to be framed open only with cmd/ctrl-click. Links printed by older versions (`/<id>/report.html`) still work, without the frame.
- Auto-start: when the server address is local (`127.0.0.1`, `localhost`, `::1`) and nothing answers, `serve` starts `sharefly server --api-addr <that address>` in the background, logs to `<data-dir>/server.log`, and waits until it answers. If the server exits during startup, `serve` (and `start`) fail at once with the last lines of its log. `ls`, `rm` and `renew` never auto-start.
- Expiry times print in local time.
- Exit codes: 0 success, 1 error (message on stderr), 2 usage error.

### Configuration

Settings live in `~/.config/sharefly/config.json` (`$XDG_CONFIG_HOME/sharefly/config.json` if set). Change them with `sharefly config set <key> <value>`; `sharefly config` shows every setting, its effective value and whether it comes from the file or the default.

| Key | Default | Used by | |
|---|---|---|---|
| `public-url` | `http://<public-addr>` | server | base URL for share links |
| `server` | `http://<api-addr>` | `serve`, `ls`, `rm`, `renew`, `stop` | server API URL to talk to, with scheme |
| `data-dir` | `~/.local/state/sharefly` (`$XDG_STATE_HOME/sharefly` if set) | server, `stop` | holds `shares/`, `tmp/`, `shares.json`, `server.pid`, `server.args.json`, `server.log` |
| `api-addr` | `127.0.0.1:7787` | server | management API listen address; the tailnet IP accepts other devices |
| `public-addr` | `127.0.0.1:7788` | server | file server listen address (what cloudflared points at) |
| `tunnel` | `off` | server | `off`, `quick` (random trycloudflare.com URL; links use it automatically) or `token` (your tunnel) |
| `tunnel-token` | none | server | Cloudflare tunnel token for `tunnel=token`; secret, shown hidden, no flag |
| `ttl` | `7d` | `serve` | default link lifetime |

- `sharefly config open` opens the file in `$VISUAL` or `$EDITOR` (e.g. `EDITOR="code --wait"`), or the system's default app if neither is set. The file lists every key with its default filled in, using underscores (`public_url`). `public_url` and `server` stay empty unless you set them, which means they follow `public_addr` and `api_addr`. After a terminal editor exits, the file is checked and a running local server restarts if a listen address or `data_dir` changed. Unknown keys and invalid values are rejected by every command except `config set` and `config open`, so you can always repair the file. A URL without a scheme is accepted: `public-url` gets `https://` and `server` gets `http://`.
- An explicit `--tunnel off` (on `serve`, `start` or `server`) means local links: the config `public-url` is ignored for that server, since nothing would serve it. `tunnel off` from the config file keeps `public-url`, for a proxy you run yourself. An explicit `--public-url` wins over both.
- Every key except `tunnel-token` has a matching flag (`--public-url`, `--server`, `--data-dir`, `--api-addr`, `--public-addr`, `--ttl`, `--tunnel`) that overrides the config file for one command, e.g. `sharefly start --tunnel quick`. The token has no flag so it never shows up in `ps`.
- A running server reads `public-url` from the file whenever the file changes, however you edit it, unless it was started with `--public-url`. Setting another server key (`data-dir`, `api-addr`, `public-addr`, `tunnel`, `tunnel-token`) restarts a running local server with the flags it was started with, so the change applies at once. Changing `data-dir` doesn't move existing shares; the command prints where they are.

### Server

`sharefly start` launches the server in the background, waits until it answers, prints its pid and log path, and returns; if one is already running it says so. `sharefly server` runs in the foreground until Ctrl-C or SIGTERM, to watch its log or to run it under your own service manager. Both read the config file and accept `--api-addr`, `--public-addr`, `--public-url`, `--data-dir` and `--tunnel`. With a tunnel, the server also runs `cloudflared`; in quick mode it waits up to 30s for the tunnel address before answering, so the first link is already public. While running, the server writes `<data-dir>/server.pid` (used by `stop`) and `<data-dir>/server.args.json` (used to restart it after a config change), and logs to `<data-dir>/server.log` when started in the background.

### HTTP API

The management API on `--api-addr` is plain HTTP + JSON. The CLI uses it; any other client can too. `GET /` serves the management page.

Browser requests from another site (an `Origin` that isn't the API's own address) get 403, so a web page you visit can't drive the API through your browser. The `Host` must be an IP address, `localhost`, a single-label name, or a `*.ts.net` / `*.local` name; other names get 403, which blocks DNS rebinding. Reaching the API under another DNS name isn't supported.

| Method | Path | Success | Errors |
|---|---|---|---|
| `POST` | `/shares?ttl=7d&name=report.html[&password=1][&once=1]` (tar.gz body; missing `ttl` = `7d`) | 201 record + `url` (+ `password` once) | 400 bad ttl, archive, `password` or `once` value, 413 body or uncompressed size over 100MB, 500 |
| `GET` | `/shares` | 200 array of record + `url` | |
| `DELETE` | `/shares/{id}` | 204 | 404, 500 |
| `GET` | `/shares/{id}/markdown` | 200 the original Markdown as an attachment | 404 (unknown share, not a Markdown share, or shared by a version without embedded Markdown) |
| `POST` | `/shares/{id}/renew` body `{"ttl":"7d"}` | 200 record + `url` | 400, 404 (also for an already-expired share), 500 |

Record: `{"id","name","entry","size","created_at","expires_at","url","protected"}`, plus `"once":true` for a one-time share (whose `expires_at` drops to a minute away when it is opened); the upload response of a protected share also has `"password"`, and no response ever has it again. `entry` is the path the `url` page shows, relative to the share (`""` = its `index.html`); `size` is uncompressed bytes; `expires_at: null` means never. Errors are `{"error":"..."}`.

The archive may hold only regular files and directories with relative paths, at most 10000 entries, and no entry may conflict with an earlier one (a file `a` followed by `a/b`).

## Good to know

- **Access:** each share gets a random 10-character ID. The unguessable link is the only access control, unless the share was served with `--password` (80-bit generated password, asked on a password page and kept in a cookie scoped to that share) or `--once` (only the opener's browser gets a cookie for it). When the server listens on its tailnet address, anyone on your tailnet can create and delete shares.
- **Expiry:** an expired share is 404 at once; its files are swept at startup and every 5 minutes. `rm` takes effect immediately; responses carry `Cache-Control: private, no-store`, so Cloudflare never serves stale copies.
- **Limits:** 100MB per upload (compressed and uncompressed), 10000 files.
- **What gets uploaded:** empty folders aren't. `.git` and `.DS_Store` are skipped. Inside a folder, symlinks and other non-regular files are skipped with a warning, and a symlinked root `index.html` is rejected. The path you pass to `serve` may itself be a symlink; it is followed.
- **Data folder:** at startup the server deletes anything under `<data-dir>/shares/` that has no record and empties `<data-dir>/tmp/`. Don't put files there by hand.
- **Network:** the file server binds `127.0.0.1` and is reached publicly only through cloudflared. With `tunnel` set, sharefly runs the `cloudflared` binary itself; it never calls the Cloudflare API.

## Development

```
make            # test + build
make check      # vet, lint, tests, gofmt check
make run        # local server with data in .bin/data
make install    # symlink .bin/sharefly into /usr/local/bin
```
