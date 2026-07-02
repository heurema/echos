# echos

Share a running coding-agent session (Claude Code / Codex) with a friend — end-to-end encrypted through an ephemeral zero-knowledge relay. One command to send, one to receive. The transcript arrives byte-identical, so `claude --resume` keeps the full history.

```
Alice:  echos send bob            → ✓ debate: "Fix acp timeout" → bob (24h)
Bob:    echos open                → from Alice ✓ · installed
                                   → resume: claude --resume a3f1c9
```

The CLI is agent-first: non-interactive, `--json` on every command, errors are ready-to-run next commands, exit codes `0` ok / `1` error / `2` needs clarification.

## Quickstart

```sh
go build -o echos       ./cmd/echos
go build -o echos-relay ./cmd/echos-relay

./echos-relay                                # relay on :8080
export ECHOS_RELAY=http://127.0.0.1:8080
```

```sh
# Bob
echos id                                     # → 19b9ff59285cd393414e

# Alice
echos friend add bob 19b9ff59285cd393414e
echos send bob                               # newest session in this directory

# Bob, inside his checkout of the project
echos inbox
echos open                                   # → resume: claude --resume <id>
```

## Commands

| Command | What it does |
|---|---|
| `echos id [--key <path>]` | Print your echo-id; identity created lazily (fresh ed25519, or reuse an SSH key via `--key`). |
| `echos friend add <name> <echo-id>` | Fetch a friend's key from the relay, verify it hashes to their echo-id, store under a local alias. |
| `echos sessions [--tool claude\|codex] [--n N]` | List local sessions, newest first. |
| `echos send <friend> [<session-id>]` | Encrypt a session to a friend and drop it in their mailbox. Defaults to the newest session in the current directory's project. |
| `echos inbox` | List pending items addressed to you. |
| `echos open [<id>] [--dir <path>] [--allow-unknown] [--resume]` | Decrypt, verify the sender, install; prints the resume command (`--resume` runs it). Defaults to the newest inbox item. |

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

Client state in `~/.config/echos/`: `identity` (0600), `identity.pub`, `friends.json`, `config.json`. Relay URL: `$ECHOS_RELAY` → `config.json` → `http://localhost:8080`.

Relay flags (env: `ECHOS_RELAY_*`):

| Flag | Default |
|---|---|
| `-addr` | `:8080` |
| `-db` | `echos-relay.db` |
| `-ttl` | `24h` |
| `-max-blob-size` | 25 MiB |
| `-rate-limit` | 10 req/min per IP on writes |
| `-sweep-interval` | `1m` |

## Development

```sh
go build ./... && go vet ./... && go test ./...
```

Design and wire format: [`docs/SPEC.md`](docs/SPEC.md). A new agent is one adapter: implement `session.Adapter` (`Discover` / `Package` / `Install` / `ResumeCommand`); envelope and relay stay untouched.
