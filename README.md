# kuticon

Command-line tool and MCP server for [Kuticon](https://kuticon.com): turn one
sentence into a 12-icon pack, or search the icon library, from your terminal
or your AI assistant.

One small binary, `kuticon`, with no dependencies. It is a plain client of
Kuticon's public HTTP API (the same one the website uses). Icons are CC0.

## Install

With Go 1.24 or newer:

```sh
go install github.com/nyancodex/kuticon-cli/cmd/kuticon@latest
```

This puts `kuticon` in `$(go env GOPATH)/bin`; make sure that is on your `PATH`.

Or download a prebuilt binary from
[GitHub Releases](https://github.com/nyancodex/kuticon-cli/releases)
(Linux, macOS and Windows, amd64 and arm64), unpack it, and put `kuticon`
on your `PATH`. Check the download against `SHA256SUMS`.

```sh
kuticon version
```

## Quickstart

```sh
# Draw a pack and unzip it into ./bakery
kuticon draw "icons for a neighborhood bakery" --style sticker --count 8 --out ./bakery

# Search the library and grab one icon
kuticon search "coffee cup"
kuticon get cupcake > cupcake.svg
kuticon get cupcake --style neon --format png --size 128 -o cupcake.png
```

## Commands

```
kuticon <command> [arguments] [flags]
```

| Command | What it does |
| --- | --- |
| `draw "sentence"` | Draw a new pack, wait for it, download and unzip it |
| `search <query>` | Search library icons |
| `get <id>` | Download one library icon as SVG or PNG |
| `styles` | List drawing styles, weights, corners and pack sizes |
| `formats` | List file formats and PNG sizes |
| `mcp` | Run the MCP server on stdin/stdout (for AI assistants) |
| `version` | Print the version |

Run `kuticon <command> --help` for the flags of one command. Flags take one
or two dashes (`-json` and `--json` are the same).

### Global flags (every command)

| Flag | Meaning |
| --- | --- |
| `--base-url URL` | Site to talk to. Default `$KUTICON_URL`, else `https://kuticon.com` |
| `--json` | Print machine-readable JSON on stdout |
| `-q`, `--quiet` | No progress on stderr |

### `kuticon draw "sentence"`

Queues a new pack, shows its place in the queue on stderr, then downloads the
zip and unzips it into `--out`.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--style` | `outline` | `outline`, `sticker`, `blueprint`, `neon`, `clay`, `pixel` |
| `--weight` | `text` | `fine`, `text`, `bold` |
| `--corner` | `round` | `round`, `sharp` |
| `--count` | `12` | Icons in the pack: 1, 4, 6, 8, 12, 16, 24 |
| `--formats` | `svg,png` | Comma list of `svg`, `png`, `font`, `react`, `vue`, `sprite`, `app` |
| `--sizes` | all | Comma list of PNG sides from `16,24,32,48,64,128,256` |
| `--out` | `./kuticon-{name}` | Directory to unzip into |
| `--zip` | off | Save the zip into `--out` instead of unzipping it |
| `--timeout` | `10m` | How long to wait for the pack |
| `--resume ID` | | Wait for an existing pack id instead of drawing a new one |

```sh
kuticon draw "a set for a plant shop" --formats svg,png,font,react --sizes 24,64,256
```

Formats: `font` is an icon font (WOFF2, TTF and CSS), `react` and `vue` are
component packs, `sprite` is an SVG sprite plus a Figma-ready sheet, `app`
is Android and iOS app-icon sizes.

#### `--resume`

A pack is never posted twice. If waiting times out (exit 6), the pack may
still finish on the site; pick it up with its id instead of drawing again:

```sh
kuticon draw --resume 0123456789abcdef0123456789abcdef
```

The id is printed when the pack is queued and in the timeout message (and in
`--json` output).

### `kuticon search <query>`

One word also matches inside longer words (`bit` finds `bitcoin`); several
words match any whole word.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--limit` | `20` | Most results, 1 to 100 |
| `--offset` | `0` | Skip this many results |

### `kuticon get <id>`

SVG goes to stdout unless `-o` is set. PNG goes to `{id}-{size}.png` unless
`-o` is set; `-o -` writes to stdout.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--style` | `outline` | `outline`, `sticker`, `blueprint`, `neon`, `clay`, `pixel` |
| `--format` | `svg` | `svg` or `png` |
| `--size` | `256` | PNG side: 16, 24, 32, 48, 64, 128, 256 |
| `-o`, `--out` | | Output file, or `-` for stdout |

### `kuticon styles`, `kuticon formats`

List what the site accepts. No flags beyond the global ones.

### `kuticon mcp`

| Flag | Default | Meaning |
| --- | --- | --- |
| `--max-wait` | `15m` | Longest wait a `draw_icon_pack` call may ask for |

See [MCP server](#mcp-server) below.

### Exit codes

| Code | Meaning |
| --- | --- |
| 0 | OK |
| 1 | Network, server or file error |
| 2 | Usage error, or a request the site refused as invalid |
| 3 | Not found |
| 4 | Daily limit reached or queue full (try later) |
| 5 | The pack failed |
| 6 | Timed out waiting (the pack may still finish; use `--resume`) |

## JSON output

Add `--json` to any command for machine-readable output on stdout. Progress
and errors always go to stderr, so stdout stays clean for `jq`:

```sh
kuticon search cat --json | jq -r '.icons[].id'
kuticon styles --json | jq '.styles[].id'
```

- `search` returns `{query, total, offset, icons: [{id, name, pack, packName, category, page, svg}]}`.
- `styles` and `formats` return the site's options: `styles`, `weights`,
  `corners`, `counts`, `formats`, `sizes`, `dailyLimit`, `promptMaxLen`.
- `draw` returns the pack: `id`, `status`, `title`, its icons, and where it
  was saved.

## MCP server

### Set up with your AI agent

The easiest way: paste this prompt into Claude Code, Codex, Cursor, VS Code
Copilot or any other coding agent, and let it do the setup.

```text
Set up the Kuticon MCP server for yourself (the agent I am talking to now).

1. Detect my OS and architecture.
2. Install the kuticon CLI. If Go 1.24+ is available, run:
     go install github.com/nyancodex/kuticon-cli/cmd/kuticon@latest
   Otherwise download the archive for my OS/arch from
   https://github.com/nyancodex/kuticon-cli/releases/latest together with
   SHA256SUMS, verify the checksum (stop if it does not match), unpack it,
   and put the kuticon binary in a user-writable directory on my PATH
   (for example ~/.local/bin, or %USERPROFILE%\bin on Windows).
   Do not use sudo or admin rights unless there is no other way, and ask me first.
3. Verify the install with: kuticon version
4. Find the absolute path of the kuticon binary (command -v kuticon, or
   where kuticon on Windows).
5. Register an MCP server named "kuticon" with command = <that absolute path>
   and args = ["mcp"], using your own native method (your CLI's "mcp add"
   command or your MCP config file). Use user/global scope unless I say
   otherwise. If a "kuticon" entry or the config file already exists, show
   me what you would change and ask before overwriting anything; back up
   any file you edit.
6. Reload or restart MCP servers if needed, then confirm it works by calling
   the kuticon list_styles tool and showing me the result.
Tell me each command you run, and do not change anything else.
```

### One-line setup per client

If `kuticon` is on your `PATH` you can also register it yourself. Use the
absolute path from `command -v kuticon` instead of `kuticon` if your client
does not see your shell's `PATH`.

**Claude Code**

```sh
claude mcp add --scope user kuticon -- kuticon mcp   # all your projects
claude mcp add kuticon -- kuticon mcp                # default scope "local": you, this project only
claude mcp add --scope project kuticon -- kuticon mcp  # writes .mcp.json to share with the team
```

Options go before the server name; everything after `--` is the server command.

**Codex CLI**

```sh
codex mcp add kuticon -- kuticon mcp
```

This writes to `~/.codex/config.toml`. The same thing written by hand (or put
it in a trusted project's `.codex/config.toml`):

```toml
[mcp_servers.kuticon]
command = "kuticon"
args = ["mcp"]
```

**Cursor**

One-click install link (Cursor asks before it adds anything):
[Add kuticon to Cursor](https://cursor.com/install-mcp?name=kuticon&config=eyJjb21tYW5kIjoia3V0aWNvbiIsImFyZ3MiOlsibWNwIl19).
The same as a deeplink (paste it into your browser):

```text
cursor://anysphere.cursor-deeplink/mcp/install?name=kuticon&config=eyJjb21tYW5kIjoia3V0aWNvbiIsImFyZ3MiOlsibWNwIl19
```

`config` is the base64 of `{"command":"kuticon","args":["mcp"]}`. Or edit
`~/.cursor/mcp.json` (all projects) or `.cursor/mcp.json` (one project) as
shown below.

**VS Code**

```sh
code --add-mcp '{"name":"kuticon","command":"kuticon","args":["mcp"]}'
```

This adds it to your user profile. On Windows `cmd`, escape the quotes:
`code --add-mcp "{\"name\":\"kuticon\",\"command\":\"kuticon\",\"args\":[\"mcp\"]}"`.

**Claude Desktop** has no command; edit its config file as shown below.

### How it works

`kuticon mcp` runs a [Model Context Protocol](https://modelcontextprotocol.io)
server over stdio (revision 2025-06-18; 2025-03-26 and 2024-11-05 when a
client asks), so assistants like Claude, Cursor and VS Code Copilot can draw
and search icons. Logs go to stderr only; stdout is the protocol.

### Tools

| Tool | What it does |
| --- | --- |
| `draw_icon_pack` | Draw a pack from a sentence. Returns every icon's SVG text, the zip download URL (also as a `resource_link`) and the pack page. Sends `notifications/progress` while queued when the client passes a `progressToken`. Default wait 300 s (`timeout_seconds` up to 900); on timeout it returns the `job_id` to resume with instead of drawing again. |
| `search_icons` | Search the library: ids, names, packs, SVG URLs. |
| `get_icon` | One library icon as SVG text, in any style. |
| `list_styles` | Styles, weights, corners, counts, formats and sizes. |

Bad arguments and site refusals (limit reached, queue full, unknown icon)
come back as tool results with `isError: true`, so the assistant can read
them.

Use the full path to `kuticon` (`which kuticon`) if your app does not see
your shell's `PATH`; this is common with Claude Desktop on macOS.

### Config files

#### Claude Desktop

`claude_desktop_config.json` (Settings → Developer → Edit Config):

```json
{
  "mcpServers": {
    "kuticon": {
      "command": "/usr/local/bin/kuticon",
      "args": ["mcp"]
    }
  }
}
```

#### Cursor

`~/.cursor/mcp.json` (all projects) or `.cursor/mcp.json` (one project):

```json
{
  "mcpServers": {
    "kuticon": {
      "command": "kuticon",
      "args": ["mcp"],
      "env": { "KUTICON_URL": "https://kuticon.com" }
    }
  }
}
```

#### VS Code

`.vscode/mcp.json` in the workspace (or "MCP: Add Server" in the command
palette):

```json
{
  "servers": {
    "kuticon": {
      "type": "stdio",
      "command": "kuticon",
      "args": ["mcp"]
    }
  }
}
```

There is no hosted MCP endpoint: the stdio server runs on your machine and
draws from your own network, so you get your own daily limit.

## Limits

- **Daily draw limit.** Every `draw` (and `draw_icon_pack`) counts against
  the site's daily limit per network, shared with the website and anything
  else on the same connection. The number is `dailyLimit` in
  `kuticon styles --json`; it resets at 00:00 UTC. When it is reached, or the
  queue is full, `draw` stops with exit 4 and tells you when to retry. The
  CLI never re-posts a pack and honours `Retry-After` while polling.
- **Search covers gallery icons only.** `search` and `search_icons` find the
  icons shown in the site's gallery categories; some icons that only appear
  in generated packs are not searchable.
- `search`, `get`, `styles` and `formats` are read-only and do not count
  against the limit.

## Building from source

```sh
git clone https://github.com/nyancodex/kuticon-cli
cd kuticon-cli
go build -o kuticon ./cmd/kuticon
go test ./...
```

Release archives for six targets (linux, darwin, windows × amd64, arm64)
land in `dist/` with a `SHA256SUMS` file:

```sh
VERSION=v0.1.0 scripts/release.sh
TARGETS="darwin/arm64" VERSION=v0.1.0 scripts/release.sh
```

## License

[MIT](LICENSE) © 2026 nyancodex. Icons you draw or download from Kuticon are CC0.
