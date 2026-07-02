# echos

**Share a running coding-agent session with a friend as easily as a chat message — one command to send, one to receive.**

`echos` hands a teammate not "here's a branch, figure it out" but the whole live context of your Claude Code or Codex session: the conversation, the decisions, the train of thought. They pick up exactly where you left off (`claude --resume`) or feed the transcript to their own agent. Everything is end-to-end encrypted and flows through an ephemeral, zero-knowledge relay that never sees your content.

```
Alice:  echos send bob            → ✓ debate: "Fix acp timeout" → bob (24h)
Bob:    echos open                → from Alice ✓ · installed
                                   → resume: claude --resume a3f1c9
```

No links to copy, no files to upload. Alice runs one command; the session appears in Bob's inbox.

---

## Why

A git branch captures *what* changed. It throws away *why* — the reasoning you and your agent worked through to get there. `echos` moves that reasoning intact, so a colleague continues the actual conversation instead of reverse-engineering a diff.

It is **agent-first**: the CLI is designed to be driven by your coding agent, not just typed by a human. Every command is non-interactive, supports `--json`, and reports errors as ready-to-run next commands. In practice the whole thing is two sentences to your agent — *"send this session to Bob"* and *"check my inbox, pick it up."*

---

## Quickstart

Build the two binaries (Go 1.26+):

```sh
git clone https://github.com/heurema/echos.git && cd echos
go build -o echos       ./cmd/echos
go build -o echos-relay ./cmd/echos-relay
```

Run a relay (or point at a shared one via `ECHOS_RELAY`):

```sh
./echos-relay            # listens on :8080 by default
export ECHOS_RELAY=http://127.0.0.1:8080
```

**Bob** creates an identity and shares his echo-id:

```sh
echos id                 # → 19b9ff59285cd393414e   (identity created lazily)
```

**Alice** adds Bob and sends the latest session in her current project:

```sh
echos friend add bob 19b9ff59285cd393414e
echos send bob           # defaults to the newest session in this directory
```

**Bob** receives it inside his checkout of the project:

```sh
echos inbox              # 1  from alice   debate   "Fix acp timeout"   2m ago
echos open               # verifies sender, installs the transcript,
                         # prints:  claude --resume a3f1c9
```

The transcript arrives **byte-identical** to Alice's original, so `claude --resume` retains her full history.

---

## Commands

| Command | What it does |
|---|---|
| `echos id [--key <path>]` | Print your echo-id. Identity is created lazily on first use — a fresh ed25519 key in `~/.config/echos/`, or an existing SSH key via `--key`. |
| `echos friend add <name> <echo-id>` | Fetch a friend's public key from the relay, verify it hashes to their echo-id, and store it under a local alias. |
| `echos sessions [--tool claude\|codex] [--n N]` | List local Claude Code / Codex sessions, newest first. |
| `echos send <friend> [<session-id>]` | Encrypt a session to a friend and drop it in their mailbox. Defaults to the newest session in the current directory's project. |
| `echos inbox` | List pending items addressed to you (sender and title shown after a client-side decrypt). |
| `echos open [<id>] [--dir <path>] [--allow-unknown] [--resume]` | Decrypt, verify the sender, and install a received session; prints the resume command (add `--resume` to run it). Defaults to the newest inbox item. |

Every command accepts `--json` for a stable, agent-consumable schema, never reads stdin, and exits `0` (ok) / `1` (error) / `2` (needs clarification).

---

## How it works

Two versioned contracts, everything else is local plumbing.

### The envelope (contract 1)

A session is packaged, signed, then encrypted — the sender's identity lives **inside** the ciphertext, so the relay can never learn who sent what.

```
[ magic "ECHO" | version 0x01 ]          ← 5-byte header, readable without decrypting
[ age ciphertext to the recipient's key:
     tar.gz
       ├── manifest.json                  ← tool, session id, project, per-file SHA-256
       ├── signature.sig                  ← ssh signature over manifest.json, by the sender
       └── <native session files, as-is> ← never normalized; resume needs exact bytes
]
```

On `open`, the recipient decrypts, recomputes every file's hash against the signed manifest, verifies the signature against their address book (`from Alice ✓`), and rejects anything with an unsafe path or an unmanifested file.

### The relay (contract 2)

A dumb, zero-knowledge mailbox. It sees ciphertext keyed by a recipient fingerprint and nothing else.

```
POST /keys                    publish your public key (so friends can resolve you)
GET  /keys/{fpr}              friend discovery
GET  /challenge?fpr={fpr}     single-use nonce for read authentication
POST /mailbox/{fpr}           drop off an envelope            → {id, ttl}
GET  /mailbox/{fpr}           list your inbox     (challenge-signed)
GET  /blob/{id}               fetch an envelope   (challenge-signed)   → 410 once expired
```

Reads are authenticated per request: you fetch a nonce, sign it with your private key, and present the signature — so only the mailbox owner can list or fetch. Blobs evaporate after their TTL (default 24h).

### Crypto & stack

No hand-rolled cryptography. Encryption is [`filippo.io/age`](https://github.com/FiloSottile/age) to the recipient's ed25519/SSH key; signatures, keys, and fingerprints go through `golang.org/x/crypto/ssh`. The CLI is [`kong`](https://github.com/alecthomas/kong); the relay is stdlib `net/http` + [`bbolt`](https://github.com/etcd-io/bbolt) + `golang.org/x/time/rate`. No new crypto, no web framework, two static binaries.

---

## Security model

- **Confidential end-to-end.** The relay and the network see only ciphertext; content, project, and sender identity stay encrypted.
- **Authenticated.** Each envelope is signed by the sender and verified against your local friends list. `open` from an unknown sender is refused by default (`--allow-unknown` to override) — nothing is installed silently.
- **No trust in the relay.** An echo-id *is* the fingerprint of the public key, so `friend add` detects any key the relay might try to substitute.
- **Ephemeral.** Mailbox blobs are TTL-bound and unfetchable once expired, even if the relay is later compromised.
- **Untrusted archives.** Extraction rejects absolute paths and `..` traversal and installs only manifested files into adapter-owned locations.

One honest caveat: a transcript can contain absolute paths, file contents, and possibly secrets. Send to people you'd share your screen with.

---

## Configuration

**Client** — local state lives in `~/.config/echos/`:

```
identity        private key (0600)
identity.pub
friends.json    your address book
config.json     { "relay_url": "…" }
```

The relay URL resolves from `$ECHOS_RELAY`, else `config.json`, else `http://localhost:8080`.

**Relay** — flags (with `ECHOS_RELAY_*` env equivalents):

| Flag | Default | |
|---|---|---|
| `-addr` | `:8080` | listen address |
| `-db` | `echos-relay.db` | bbolt store path |
| `-ttl` | `24h` | default mailbox TTL |
| `-max-blob-size` | `25 MiB` | reject larger envelopes |
| `-rate-limit` | `10` | requests/min per source IP on writes |
| `-sweep-interval` | `1m` | expired-blob purge cadence |

---

## Development

```sh
go build ./...
go vet ./...
go test ./...
```

The full design and wire format live in [`docs/SPEC.md`](docs/SPEC.md).

Supporting a new agent is one adapter: implement the `session.Adapter` seam (`Discover` / `Package` / `Install` / `ResumeCommand`) and the envelope and relay layers are untouched.

---

## Status

`echos v0` — the core is complete and exercised end-to-end: identity, friends, `send`/`inbox`/`open`, encrypted round-trips through a live relay, sender verification, and safe-default refusals. Deliberately out of scope for now: normalizing transcripts between tools, live session sync, transferring repository state, and accounts/long-term storage. Second-ring conveniences (`send --link`, `--scrub`, `inbox --watch`, `friend list/rm`) are additive and don't touch the core.
