# Echos — Specification

> Share a running agent session with a friend as easily as a chat message:
> one command on your side, one on theirs.

Status: design locked, implementation not started.

---

## 1. Product

### Value

A developer hands a colleague not "here's a branch, figure it out" but live
context — the whole Claude Code / Codex session: the conversation with the
agent, the decisions, the train of thought. The recipient continues from the
same point (`claude --resume`) or feeds the transcript to their own agent as
context.

### Principles

1. **Agent-first.** The primary CLI consumer is not a human but their agent.
   No interactivity: no pickers, no `[Y/n]`, never block on stdin.
2. **Happy path with no arguments.** The two daily verbs — `send` and
   `open` — work with no id and no flags in the typical case.
3. **Zero-knowledge relay.** The server sees only ciphertext blobs and
   fingerprint mailboxes. It never sees content, projects, or senders.
4. **Ephemerality.** Default TTL 24 hours; the parcel evaporates from the
   server.
5. **Courier, not translator.** Transcripts travel as native bytes, no format
   normalization.
6. **Errors are an API.** Every error carries the next command. The agent
   self-heals, the human copies the hint.

### End-to-end flow

```
Bob:    echos id                          → echo:7f3ka9c2   (tells Alice in chat)
Alice:  echos friend add bob echo:7f3ka9c2
        echos send bob                    → ✓ debate: "Fix acp timeout" → bob (24h)
Bob:    echos open                        → from Alice ✓ · installed
                                          → resume: claude --resume a3f1c9
```

Through agents it's the same — two sentences:
Alice inside a session: *"send this session to Bob"* → the agent runs
`echos send bob` (the latest session in the current cwd == its own).
Bob: *"check the inbox, pick it up"* → `echos inbox --json` → `echos open --json`.

Onboarding asymmetry: to **receive**, you configure nothing — only the sender
adds a friend. A recipient who hasn't added Alice simply sees `from unknown`
instead of `from Alice ✓` (and a refusal without `--allow-unknown`).

---

## 2. CLI

The binary is called `echos` (not `echo` — collides with the shell builtin).

### Core (v0)

```
echos id                                  # my echo-id; identity created lazily, idempotent
echos friend add <name> <echo-id>         # add a friend (fetches pubkey from relay, verifies fpr)
echos sessions [--json] [--tool claude|codex] [-n 20]
                                          # local sessions, newest first
echos send <friend> [session-id] [--json] [--ttl 24h]
                                          # default session: latest in the current cwd
echos inbox [--json]                      # incoming (observe)
echos open [id] [--dir <path>] [--json] [--allow-unknown] [--resume]
                                          # act; default: newest in inbox, dir=cwd
```

### Second ring (additive, does not touch the core)

`friend list/rm` · `send --link` (secret link for non-users, key in
`#fragment`) · `send --pick` · `send --once` · `send --scrub` ·
`inbox --watch` (long-poll) · exporters (`--md`).

### Behavior contract

- **Exit codes:** `0` ok · `1` error · `2` needs clarification (options +
  a ready-to-run command for each are printed).
- **`--json`** on every command — a stable schema for agents. Without the
  flag, in a TTY — tables for humans.
- **No exec by default.** `open` installs and *prints* the resume command.
  `--resume` is an explicit opt-in for humans.
- **`--help` is documentation for the agent.** Written as a spec, with
  examples.
- **Safe default:** `open` from a sender not in your friends — refusal
  (exit 1, fingerprint, hint). Nothing is installed silently.
- stdin is never read; stderr is diagnostics; in non-TTY no color/spinners.

### Errors (contract examples)

```
echos send bob    → no friend "bob" — run: echos friend add bob echo:…
echos send bob    → no sessions in this dir; recent: [list] — echos send bob <id>
echos open        → inbox is empty
echos open        → sender unknown (echo:9f3a…) — add friend or pass --allow-unknown
```

---

## 3. Architecture

Two **versioned contracts** + local plumbing. Everything that is not a
contract evolves freely.

```
cmd/echos                CLI (argv → stdout/exit code, no TUI dependencies)
  internal/session/      tool adapters → common Session
  internal/identity/     key + friends (~/.config/echos/)
  internal/envelope/     contract 1: packing/encryption/signing
  internal/relay/        http client for contract 2
cmd/echos-relay          server: mailbox store with TTL
```

### Stack

- Go, one go.mod, two binaries. Crypto via libraries, not shell-out:
  - `filippo.io/age` + `filippo.io/age/agessh` — encrypt to the recipient's
    ed25519/ssh key, decrypt with your own key;
  - `golang.org/x/crypto/ssh` — sign/verify the sender;
  - `net/http` (stdlib) — client and server, no frameworks;
  - `go.etcd.io/bbolt` — relay store with a TTL sweeper;
  - argv — stdlib `flag`; no interactivity, no need for cobra.

### Identity

- Default `init` (lazy): a **fresh ed25519 dedicated to echos** in
  `~/.config/echos/identity`. Not deploy keys: rotating an SSH key must not
  break your echos identity, and compromising the echos key must not grant
  server access.
- `--key ~/.ssh/id_ed25519` — an option to reuse an existing SSH key.
- **echo-id = a short fingerprint of the public key** (e.g. `echo:7f3ka9c2`)
  — self-authenticating, as in SSH/Signal. Names ("bob") are local aliases in
  the address book and never go on the wire.
- On identity creation the pubkey is published to the relay → `friend add`
  fetches the key by echo-id and **verifies it hashes to that same
  fingerprint** — the relay has nothing to swap, it is irrelevant to trust.
  Fallback for the paranoid: a card (pubkey as a string) over any channel.

### Local state

```
~/.config/echos/
  identity            private key (0600)
  identity.pub
  friends.json        [{name, echo_id, pubkey, added_at}]
  config.json         {relay_url}          # override: ECHOS_RELAY
```

---

## 4. Contract 1: Envelope (parcel format)

```
[magic "ECHS" | version u8 = 1]
[age ciphertext to the recipient's pubkey:
    tar.gz
      ├── manifest.json
      ├── files/…                  # native session bytes, as-is
      └── signature                # ssh signature over manifest+files by the sender's key
]
```

**The sender and their signature are inside the ciphertext.** By construction
the relay does not know who is sending. The recipient learns `from Alice ✓`
after decryption, by checking the signature against their address book.

### manifest.json

```json
{
  "echos": 1,
  "tool": "claude",
  "session_id": "a3f1c9-…",
  "title": "Fix acp backend timeout",
  "project_hint": "personal/debate",
  "created": "2026-07-01T20:15:00Z",
  "sender": {"echo_id": "echo:ab12cd9f", "pubkey": "ssh-ed25519 AAAA…"},
  "files": [{"path": "a3f1c9.jsonl", "role": "transcript"},
            {"path": "a3f1c9/subagents/…", "role": "aux"}]
}
```

Extension without breakage: new fields (`git_rev`, `scrubbed`) are ignored by
old clients; an unknown `tool` is handled by degradation (see §6).

**Transcripts are not normalized.** Resumability is sacred: `claude --resume`
requires its exact jsonl byte-for-byte. Format conversion is a lossy
reverse-engineering effort that breaks with every tool release. Cross-tool is
solved by the agent reading the other transcript, not by conversion.

Claude sessions can be **multi-file** (next to `<uuid>.jsonl` there's a
`<uuid>/subagents/`) — so the container must be a tar, not a single file.
Gzip is mandatory: transcripts can be 5+ MB.

We share a **snapshot** at the moment of send; live appends after sending are
not our concern.

---

## 5. Contract 2: Relay API

Mailbox = fingerprint of the recipient's public key. No accounts, passwords,
or sessions.

```
POST /keys                     body: pubkey                 → 201            # publish on init
GET  /keys/{fpr}                                            → pubkey         # friend discovery
POST /mailbox/{fpr}            body: envelope  ?ttl=24h     → {"id": "…"}    # drop off
GET  /mailbox/{fpr}            auth: challenge signature    → [{id, size, created, expires}]
GET  /blob/{id}                auth: challenge signature    → envelope       # 410 if expired
```

- **Reading a mailbox** — sign a challenge with the private key; the relay
  verifies it against the published pubkey for that fpr. Anyone can drop into
  any mailbox (spam is bounded by rate-limit and size).
- TTL: default 24h, max set by the server. A sweeper deletes expired items.
- Blob size limit, rate-limit on POST.
- The relay sees: mailbox fpr, size, time. It does not see: content, sender,
  project.
- Hosting: a single Go binary on any VPS; the same contract can sit on
  Cloudflare Workers + R2 if desired.

---

## 6. Tool adapters

All tool specifics are locked into one layer at the edges; the envelope and
relay do not know what "claude" is.

```go
type Tool interface {
    Discover() []Session          // find local sessions
    Package(s Session) []File     // which files make up the session
    Install(pkg Package, dir string) error   // put in the right place
    ResumeCommand(s Session) string          // "claude --resume X"; "" if unsupported
}
```

Supporting a new agent (gemini-cli, aider, …) = one adapter in the client;
the wire format is untouched.

### Known on-disk layouts

| Tool | Location | Notes |
|---|---|---|
| claude | `~/.claude/projects/<enc-cwd>/<uuid>.jsonl` (+ `<uuid>/subagents/`) | dir name = cwd with slashes→dashes; cwd and title extracted from the jsonl head |
| codex | `~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl` | ready-made index `~/.codex/session_index.jsonl`: `{id, thread_name, updated_at}` |

### `open` matrix (degradation without a dead end)

| Recipient has | Result |
|---|---|
| the same tool | installed in the right place + resume command — full cycle |
| a different tool | files saved, path printed → agent reads the transcript as context |
| a tool echos doesn't know | same: files + manifest.json |

`open` **never fails because of an unknown tool** — the worst outcome is
"here are the files", which is a working result for an agent.

Subtlety of installing claude sessions: resume is path-bound — the
destination folder is encoded from the *recipient's* project directory. So
`open` is run inside your project checkout (or you pass `--dir`); there is no
interactive prompt.

---

## 7. Security model

- **Confidentiality:** e2e — age to the recipient's key; the relay and the
  network see ciphertext.
- **Authenticity:** the sender's ssh signature inside the envelope; checked
  against the local address book → `from Alice ✓`.
- **No trust in the relay required:** echo-id = fingerprint, `friend add`
  verifies the received key against it; a key swap by the relay is detected
  by the client.
- **Non-friend → refusal by default** (`--allow-unknown` is an explicit
  opt-in); the agent does not silently install content from an unknown key.
- **Ephemerality:** TTL 24h; expired items are unfetchable even if the relay
  is compromised after the fact.
- **Residual risk, stated honestly:** a transcript contains absolute paths,
  file contents, possibly secrets. `--scrub` (second ring) cleans the
  obvious; responsibility for "who I send to" is on the sender.

---

## 8. Non-goals

- Normalizing/converting transcripts between tools.
- Live session sync (we share a snapshot).
- Transferring code/repository (repo state is the humans' concern; later —
  `git_rev` in the manifest and a divergence warning).
- Accounts, server-side key storage, long-term retention.

---

## 9. Implementation plan

1. **`sessions`** — discovery adapters (claude, codex), output, `--json`.
   Immediate visible result, zero external dependencies.
2. **`id` + identity** — lazy key generation, fingerprint.
3. **Envelope** — pack/encrypt/sign, unpack/verify/decrypt (+ roundtrip unit
   tests).
4. **Relay** — server (bbolt, TTL, challenge-auth) + client; local on
   `localhost:8080`.
5. **`send` / `inbox` / `open` / `friend add`** — assemble the full core.
6. Second ring as needed.
