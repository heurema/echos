# echos

Share a running coding-agent session (Claude Code / Codex) with a friend — end-to-end encrypted through an ephemeral zero-knowledge relay. One command to send, one to receive. The transcript arrives byte-identical, so `claude --resume` keeps the full history.

You hand a teammate not "here's a branch, figure it out" but the whole live context of your session — the conversation, the decisions, the reasoning — and they pick up exactly where you left off.

**Hosted relay: https://echos.heurema.dev** — the CLI uses it by default, so there's nothing to run.

```
Alice:  echos send bob            → ✓ debate: "Fix acp timeout" → bob (24h)
Bob:    echos open                → from Alice ✓ · installed
                                   → resume: claude --resume a3f1c9
```

No links to copy, no files to upload. Alice runs one command; the session lands in Bob's inbox.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/heurema/echos/main/install.sh | sh
```

Installs the `echos` binary to `~/.local/bin` (override with `ECHOS_INSTALL_DIR`). Or with Go:

```sh
go install github.com/heurema/echos/cmd/echos@latest
```

## Try it

The CLI talks to the hosted relay out of the box — no server to run.

**Bob** creates an identity and shares his echo-id (a short fingerprint of his key):

```sh
echos id                 # → 19b9ff59285cd393414e   (send this to Alice, any channel)
```

**Alice** adds Bob and sends the latest session in her current project:

```sh
echos friend add bob 19b9ff59285cd393414e
echos send bob           # defaults to the newest session in this directory
```

**Bob**, inside his checkout of the project, receives it:

```sh
echos inbox              # 1  from alice   debate   "Fix acp timeout"   2m ago
echos open               # verifies the sender, installs the transcript,
                         # prints:  claude --resume a3f1c9
```

The transcript arrives **byte-identical** to Alice's original. For a two-way channel, Bob adds Alice too (`echos friend add alice <her-echo-id>`); a sender who isn't in your friends is refused unless you pass `--allow-unknown`.

The CLI is **agent-first**: non-interactive, `--json` on every command, errors are ready-to-run next commands, exit codes `0` ok / `1` error / `2` needs clarification. In practice it's two sentences to your coding agent — *"send this session to Bob"* and *"check my inbox, pick it up."*

## Commands

| Command | What it does |
|---|---|
| `echos id [--key <path>]` | Print your echo-id; identity created lazily (fresh ed25519, or reuse an SSH key via `--key`) and (re)published to the relay. |
| `echos friend add <name> <echo-id>` | Fetch a friend's key from the relay, verify it hashes to their echo-id, store under a local alias. |
| `echos friend list` | List saved friends. |
| `echos friend rm (remove) <name>` | Remove a friend by local alias. |
| `echos sessions [--tool claude\|codex] [--n N]` | List local sessions, newest first. |
| `echos send <friend> [<session-id>]` | Encrypt a session to a friend and drop it in their mailbox. Defaults to the newest session in the current directory's project. |
| `echos inbox` | List pending items addressed to you. |
| `echos open [<id>] [--dir <path>] [--allow-unknown] [--resume]` | Decrypt, verify the sender, install; prints the resume command (`--resume` runs it). Defaults to the newest inbox item. |

Every command accepts `--json`, never reads stdin.

## How it works

**Envelope** — packaged, signed, then encrypted; the sender's identity lives inside the ciphertext, so the relay never sees it:

```
[ magic "ECHO" | version 0x01 ]
[ age ciphertext to the recipient's key:
     tar.gz
       ├── manifest.json      tool, session id, per-file SHA-256
       ├── signature.sig      ssh signature over manifest.json
       └── <native session files, as-is>
]
```

**Relay** — a zero-knowledge mailbox; reads are authenticated per request with a signed single-use nonce; blobs expire after their TTL (default 24h):

```
POST /keys                    publish your public key
GET  /keys/{fpr}              friend discovery
GET  /challenge?fpr={fpr}     nonce for read auth
POST /mailbox/{fpr}           drop off an envelope        → {id, ttl}
GET  /mailbox/{fpr}           list inbox      (challenge-signed)
GET  /blob/{id}               fetch envelope  (challenge-signed) · 410 once expired
```

## Security

- End-to-end encrypted; the relay stores only ciphertext keyed by recipient fingerprint.
- Envelopes are signed and verified against your local friends list; unknown senders are refused by default (`--allow-unknown` to override).
- echo-id *is* the key fingerprint, so a relay-substituted key is detected at `friend add`.
- Extraction rejects absolute paths, `..` traversal, and unmanifested files.
- Caveat: transcripts can contain paths, file contents, and secrets — send only to people you trust.

## Configuration

Client state in `~/.config/echos/`: `identity` (0600), `identity.pub`, `friends.json`, `config.json`. Relay URL resolves as `$ECHOS_RELAY` → `config.json` → `https://echos.heurema.dev` (the hosted default).

## Self-hosting the relay

Prefer your own relay? Run the second binary and point the CLI at it:

```sh
go build -o echos-relay ./cmd/echos-relay
./echos-relay                                # :8080; -ttl, -max-blob-size, -rate-limit, -db
export ECHOS_RELAY=http://127.0.0.1:8080
```

## Development

```sh
go build ./... && go vet ./... && go test ./...
```

Design and wire format: [`docs/SPEC.md`](docs/SPEC.md). A new agent is one adapter: implement `session.Adapter` (`Discover` / `Package` / `Install` / `ResumeCommand`); envelope and relay stay untouched.
