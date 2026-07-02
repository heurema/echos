# Executor Prompt

This prompt is prepared from an approved Pactum contract.
This prompt is prepared for the selected built-in agent when `pactum execute run` is used.
Pactum records execution artifacts and validates contract and memory boundaries before execution.

## Contract status
- Run: run_20260702_085315
- Approval: approved
- Contract hash: 1de3edbb4f937455ead7352eec2d708b118b5b4c1af4dc72e5bf1226ffa5616b

## Goal
Deliver echos v0 per SPEC.md: an agent-first CLI that shares a running coding-agent session (Claude Code / Codex) with a friend end-to-end encrypted through an ephemeral zero-knowledge relay — one command to send, one to receive. Honor the two versioned contracts (Envelope v1, Relay API) and the core principles: agent-first / no interactivity, courier-not-translator (native transcript bytes), zero-knowledge relay, ephemerality.

## In scope
- Session discovery adapters exposing a common Session model: Claude Code (~/.claude/projects/<enc-cwd>/<uuid>.jsonl, including the optional <uuid>/subagents/ subtree) and Codex (~/.codex/session_index.jsonl + rollout files), surfaced by `echos sessions [--json]` newest-first with tool, id, project, title, updated.
- Lazy identity: a fresh ed25519 key at ~/.config/echos/identity (0600), exposed by an idempotent `echos id`; optional reuse of an existing SSH key via --key; on identity creation the public key is published to the relay (POST /keys) so friends can resolve it by echo-id. Fingerprint derivation: fingerprint = lowercase hex-encoded SHA-256 digest of the ed25519 public key's SSH wire-format bytes (golang.org/x/crypto/ssh PublicKey.Marshal() output); echo-id is the first 20 hex characters (80 bits) of that fingerprint, and the relay's {fpr} path parameter is this same 20-character echo-id string — echo-id and fpr are identical values derived identically by every party, so a friend's independently-computed hash of the fetched key must equal the echo-id given at `friend add` time.
- Envelope contract v1 (wire format, byte-exact): a 5-byte header consisting of the literal ASCII magic bytes "ECHO" (0x45 0x43 0x48 0x4F) followed by a single version byte 0x01, then immediately (no length prefix) the age ciphertext produced by encrypting to the recipient's ed25519/SSH public key via filippo.io/age/agessh; the age plaintext is a gzip'd tar (tar.gz) containing exactly: manifest.json, signature.sig, and the native session files at the tool adapter's declared relative paths. manifest.json schema: {"version":1, "tool":"claude"|"codex", "session_id":string, "project":string, "title":string, "sender_echo_id":string, "sender_fingerprint":string, "created_at":RFC3339 string, "files":[{"path":string, "size":int, "sha256":hex string}, ...]} — files[] lists every native file in the archive with its relative path (as used by that tool adapter's Install) and a SHA-256 of its exact bytes. Signing input and domain: signature.sig contains a golang.org/x/crypto/ssh Signer.Sign() signature (marshaled via ssh.Marshal) computed over the raw serialized bytes of manifest.json itself (not the native files directly — their integrity is bound transitively via the files[].sha256 entries manifest.json contains); verification recomputes SHA-256 over each unpacked native file and confirms it matches the corresponding files[].sha256 entry, then verifies signature.sig against manifest.json's bytes using the sender's public key from friends.json, rejecting the envelope if either check fails. The sender identity (sender_echo_id/sender_fingerprint in manifest.json) and signature.sig live INSIDE the age ciphertext so the relay never sees them.
- Zero-knowledge relay binary (cmd/echos-relay) implementing Relay API v1 over JSON (except raw blob bodies) with these exact request/response shapes: POST /keys — request body {"fingerprint":string, "public_key":base64 string of the SSH wire-format public key}; response 201 (or 200 if the fingerprint is already registered with an identical key) {"fingerprint":string, "created":bool}; 409 if the fingerprint already exists with a different key. GET /keys/{fpr} — response 200 {"fingerprint":string, "public_key":base64 string}; 404 {"error":string} if unknown. GET /challenge?fpr={fpr} — response 200 {"nonce":base64 string of 32 random bytes, "expires_at":RFC3339 string} with a default 60s TTL from issuance; 404 if fpr is unknown. POST /mailbox/{fpr} — request body is the raw envelope bytes (application/octet-stream); response 201 {"id":string blob id, "ttl":int seconds, "expires_at":RFC3339 string}; 413 {"error":string} if the body exceeds the configured max size (persists nothing); 429 {"error":string} if the per-source-IP rate limit is exceeded. GET /mailbox/{fpr} — requires challenge-signature auth headers (X-Echos-Fingerprint, X-Echos-Nonce, X-Echos-Signature); response 200 with a JSON array of {"id":string, "size":int, "received_at":RFC3339 string, "expires_at":RFC3339 string} metadata (ciphertext itself is fetched separately via GET /blob/{id}); 401 {"error":string} on missing/invalid/expired/replayed auth. GET /blob/{id} — requires the same challenge-signature auth headers, authenticated as the blob's recipient fingerprint; response 200 application/octet-stream raw envelope bytes; 401 {"error":string} on auth failure; 404 if the id is unknown; 410 {"error":string} if the blob has expired. Challenge-signature auth required on both reads (GET /mailbox and GET /blob), with GET /challenge issuing a short-lived (default 60s TTL) single-use nonce that the client signs to authenticate those reads, rejected with 401 once expired or already consumed by a prior request; bbolt-backed store with a TTL sweeper, default TTL 24h; POST /mailbox/{fpr} rejects blobs above a configurable max size (default 25MiB) with HTTP 413, and POST /keys and POST /mailbox/{fpr} are rate-limited per source IP (default 10 requests/minute) returning HTTP 429 when exceeded, both limits configurable via relay startup flags/env vars.
- Friends: `echos friend add <name> <echo-id>` fetches the pubkey from the relay and verifies it hashes to the given fingerprint; friends stored in ~/.config/echos/friends.json as local name->identity aliases.
- Core verbs: `echos send <friend> [session-id]` (default session = latest in the current cwd project) uploads an envelope addressed to the friend's fingerprint and prints a success line with TTL; `echos inbox` lists pending items by fetching each pending blob's relay metadata (id, size, received_at, expires_at) via GET /mailbox/{fpr} and then, since sender identity and tool are not known to the relay, age-decrypting each blob via GET /blob/{id} with the local identity's private key to read manifest.json for from_fingerprint, tool, and (via a friends.json lookup) from_name — this decrypt-for-listing does not verify the sender's signature or install any files, it is a read-only enrichment of the listing, distinct from `echos open`'s full verify+install; `echos open [id] [--dir]` (default = newest inbox item, dir=cwd) decrypts, verifies the sender against the address book, installs native files, and prints the resume command.
- Behavior contract enforced across all commands: never read stdin / no interactivity, --json with a stable schema on every command, exit codes 0 ok / 1 error / 2 needs-clarification, every error prints a ready-to-run next command, no exec by default (open prints the resume command; --resume opts in), and open refuses a sender not in friends unless --allow-unknown.
- open degradation matrix: same tool -> install into the right location plus resume command; a different or unknown tool -> save the files and print their path; open never dead-ends on an unknown tool.
- Tool-adapter seam (Discover/Package/Install/ResumeCommand) that isolates all tool specifics so a new agent is one adapter and the wire format is untouched.

## Out of scope
- Normalizing or converting transcripts between tools — resumability requires the native jsonl byte-for-byte.
- Live session synchronization — echos shares only a snapshot captured at send time.
- Transferring code or repository state (a later manifest git_rev + divergence warning is explicitly deferred).
- Accounts, or long-term retention of message/session content on the relay — this does not include the public-key directory (POST/GET /keys) needed for echo-id resolution or the TTL-bound mailbox/blob store, both of which are in scope-in.
- Second-ring features (send --link secret links, --scrub, --once, inbox --watch long-poll, friend list/rm, markdown exporters) — additive and out of the v0 core.

## Acceptance criteria
- echos sessions --json returns Claude and Codex sessions merged and sorted newest-first, each entry carrying tool, id, project, title, and updated; discovery tolerates missing ~/.claude or ~/.codex without erroring (covered by TestSessionsDiscovery).
- echos id is idempotent and prints a stable echo-id; invoking any command that requires an identity with no prior identity lazily creates the ed25519 key at ~/.config/echos/identity with 0600 permissions, while pure-local discovery (echos sessions) never creates a key as a side effect (covered by TestIdentityLifecycle).
- Envelope round-trip is lossless: pack->sign->encrypt followed by decrypt->unpack->verify reproduces the exact native session bytes, and a tampered envelope or a wrong-recipient key fails to open (covered by unit tests).
- A Claude Code session that includes its optional <uuid>/subagents/ subtree round-trips losslessly through pack->sign->encrypt->decrypt->unpack->verify, and echos open installs the subagents subtree alongside the main transcript so claude --resume <id> retains the subagent history.
- Against a locally running echos-relay, with bob added as a friend and run inside a project directory that has a session, echos send bob uploads one envelope addressed to bob's fingerprint and prints a success line including the TTL.
- As bob, with alice present in bob's friends (mutual add), echos inbox lists the pending item and echos open decrypts it, verifies and shows from Alice, installs the transcript so claude --resume <id> works, and prints that resume command WITHOUT executing it.
- Against a locally running echos-relay, sending a Codex session end-to-end (alice sends, bob receives) results in echos open installing the native rollout file under ~/.codex/sessions/ and updating ~/.codex/session_index.jsonl on bob's machine, then printing codex resume <id> WITHOUT executing it.
- The open degradation matrix is exercised on both branches: opening an envelope whose packaged tool matches an adapter available on the receiving side installs the native files into that adapter's expected location and prints its tool-specific resume command; opening an envelope packaged by a tool with no matching adapter on the receiving side (or an unrecognized tool identifier) saves the received files to disk, prints their path in place of a resume command, and exits 0 without an unhandled error.
- echos open from a sender absent from friends exits 1 with the sender fingerprint and a hint and installs nothing, unless --allow-unknown is passed (covered by TestOpenRejectsUnknownSender).
- Mailbox blobs on the relay are ciphertext only, keyed by recipient fingerprint; an envelope drop carries no sender identity, project, or plaintext, and the relay stores no plaintext beyond published public keys; a GET for an expired blob returns HTTP 410 (covered by TestRelayZeroKnowledgeStorageAndExpiry).
- GET /mailbox/{fpr} and GET /blob/{id} reject requests lacking a valid challenge-signature, or bearing an incorrect one, with an authentication error and disclose no ciphertext; only a request signed by the matching private key succeeds.
- A GET /mailbox or GET /blob request signed with a challenge nonce older than the 60s TTL, or reusing a nonce already consumed by a prior successful request, is rejected with an authentication error and discloses no ciphertext; each challenge nonce authenticates at most one successful request (covered by TestChallengeNonceExpiryAndReplay).
- POST /mailbox/{fpr} rejects an envelope blob exceeding the configured max size (default 25MiB) with HTTP 413 and persists nothing; POST /keys and POST /mailbox/{fpr} requests exceeding the configured per-source-IP rate limit (default 10 requests/minute) receive HTTP 429; both limits are configurable at relay startup via flags/env vars (covered by TestRelayLimits).
- Every core command accepts --json with a stable schema, reads nothing from stdin, and on an error path prints a ready-to-run next command (exiting 2 when the situation needs clarification) (covered by TestCLIBehaviorContract).
- Archive safety: `echos open` treats packaged files as untrusted — it rejects any entry with an absolute path or a `..` traversal component and confines extraction to adapter-owned relative locations under the target dir, installing nothing when a malicious path is detected (covered by a named test).
- Each core command's --json output has a fixed, documented schema covering at minimum: `echos id --json` -> {echo_id, public_key_fingerprint, created}; `echos friend add --json` -> {name, echo_id, fingerprint}; `echos send --json` -> {friend, echo_id, blob_id, ttl, expires_at}; `echos inbox --json` -> a list of {id, from_fingerprint, from_name (if known), tool, received, ttl}; `echos open --json` -> {id, from, tool, installed_path, resume_command, degraded (bool)}; these field sets are asserted so the schemas cannot silently drift (covered by TestJSONOutputSchemas).
- Identity creation publishes the new public key to the relay via POST /keys, and a subsequent GET /keys/{fpr} against the same relay returns that key; `echos friend add <name> <echo-id>` fetches the pubkey from the relay, verifies it hashes to the given echo-id, stores the friend on success, and fails (without storing a friend) when the fetched key does not hash to the given echo-id (covered by TestKeyPublicationAndFriendResolution).
- With two or more project directories that each contain at least one Claude or Codex session, running `echos send <friend>` with no session-id argument from inside one specific project directory selects only the newest session belonging to that cwd's project, never a newer session that belongs to a different project directory (covered by TestSendDefaultsToCwdProjectLatestSession).
- An Envelope v1 blob begins with the literal magic+version header bytes, readable without performing age decryption, followed by the age ciphertext; only after successful age-decryption does the tar.gz payload yield manifest.json, the native session files, and the sender's ssh-signature, and none of the sender identity, signature, or manifest is recoverable from the header or from the age ciphertext without the recipient's private key (covered by TestEnvelopeHeaderAndInternalSignature).

## Validation commands
- go build ./...
- go vet ./...
- go test ./...
- sh -c 'grep -rl "func TestEnvelopeRoundTrip(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestEnvelopeRoundTrip$" -v | grep -q -- "--- PASS: TestEnvelopeRoundTrip"'
- sh -c 'grep -rl "func TestSubagentsSubtreeRoundTrip(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestSubagentsSubtreeRoundTrip$" -v | grep -q -- "--- PASS: TestSubagentsSubtreeRoundTrip"'
- sh -c 'grep -rl "func TestRelayAuthRejectsUnsignedOrInvalidReads(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestRelayAuthRejectsUnsignedOrInvalidReads$" -v | grep -q -- "--- PASS: TestRelayAuthRejectsUnsignedOrInvalidReads"'
- sh -c 'grep -rl "func TestEndToEndSendInboxOpen_Claude(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestEndToEndSendInboxOpen_Claude$" -v | grep -q -- "--- PASS: TestEndToEndSendInboxOpen_Claude"'
- sh -c 'grep -rl "func TestEndToEndSendInboxOpen_Codex(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestEndToEndSendInboxOpen_Codex$" -v | grep -q -- "--- PASS: TestEndToEndSendInboxOpen_Codex"'
- sh -c 'grep -rl "func TestOpenDegradationMatrix(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestOpenDegradationMatrix$" -v | grep -q -- "--- PASS: TestOpenDegradationMatrix"'
- sh -c 'grep -rl "func TestOpenRejectsUnsafeArchivePaths(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestOpenRejectsUnsafeArchivePaths$" -v | grep -q -- "--- PASS: TestOpenRejectsUnsafeArchivePaths"'
- sh -c 'grep -rl "func TestJSONOutputSchemas(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestJSONOutputSchemas$" -v | grep -q -- "--- PASS: TestJSONOutputSchemas"'
- sh -c 'grep -rl "func TestKeyPublicationAndFriendResolution(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestKeyPublicationAndFriendResolution$" -v | grep -q -- "--- PASS: TestKeyPublicationAndFriendResolution"'
- sh -c 'grep -rl "func TestSendDefaultsToCwdProjectLatestSession(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestSendDefaultsToCwdProjectLatestSession$" -v | grep -q -- "--- PASS: TestSendDefaultsToCwdProjectLatestSession"'
- sh -c 'grep -rl "func TestEnvelopeHeaderAndInternalSignature(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestEnvelopeHeaderAndInternalSignature$" -v | grep -q -- "--- PASS: TestEnvelopeHeaderAndInternalSignature"'
- sh -c 'grep -rl "func TestSessionsDiscovery(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestSessionsDiscovery$" -v | grep -q -- "--- PASS: TestSessionsDiscovery"'
- sh -c 'grep -rl "func TestIdentityLifecycle(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestIdentityLifecycle$" -v | grep -q -- "--- PASS: TestIdentityLifecycle"'
- sh -c 'grep -rl "func TestOpenRejectsUnknownSender(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestOpenRejectsUnknownSender$" -v | grep -q -- "--- PASS: TestOpenRejectsUnknownSender"'
- sh -c 'grep -rl "func TestRelayZeroKnowledgeStorageAndExpiry(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestRelayZeroKnowledgeStorageAndExpiry$" -v | grep -q -- "--- PASS: TestRelayZeroKnowledgeStorageAndExpiry"'
- sh -c 'grep -rl "func TestCLIBehaviorContract(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestCLIBehaviorContract$" -v | grep -q -- "--- PASS: TestCLIBehaviorContract"'
- sh -c 'grep -rl "func TestChallengeNonceExpiryAndReplay(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestChallengeNonceExpiryAndReplay$" -v | grep -q -- "--- PASS: TestChallengeNonceExpiryAndReplay"'
- sh -c 'grep -rl "func TestRelayLimits(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestRelayLimits$" -v | grep -q -- "--- PASS: TestRelayLimits"'

## Assumptions
- Go 1.26 toolchain. Cryptography is never hand-rolled: envelope encryption is delegated entirely to filippo.io/age + filippo.io/age/agessh (encrypt to the recipient's ed25519/SSH key, stream large transcripts), and all key handling, sender signatures, fingerprints, and optional ssh-agent access go through golang.org/x/crypto/ssh — both used as libraries, never shelling out to the age or ssh-keygen binaries.
- The CLI is built on github.com/alecthomas/kong (consistent with the sibling pactum/debate projects and already in the module cache), superseding the tentative stdlib flag mention in SPEC.md; kong provides declarative struct-tag commands, generated --help that doubles as agent documentation, and explicit exit-code control for the 0/1/2 contract.
- The relay uses only the Go stdlib net/http (1.22 method+wildcard ServeMux, no external router) plus go.etcd.io/bbolt as a single-file store with a sweeper goroutine for TTL (bbolt has no native expiry) and golang.org/x/time/rate for POST rate-limiting (default 10 requests/minute per source IP, configurable), with a default max blob size of 25MiB enforced on POST /mailbox/{fpr} (HTTP 413 when exceeded, configurable); the envelope container uses stdlib archive/tar + compress/gzip, and blob ids come from crypto/rand + encoding/base32 (no UUID dependency).
- The challenge-signature auth protocol for GET /mailbox/{fpr} and GET /blob/{id} works as: the client first fetches a short-lived, single-use challenge nonce from the relay (GET /challenge?fpr={fpr}), signs that nonce with the requester's ed25519 private key, and re-issues the GET request carrying the fingerprint, nonce, and signature as request headers (X-Echos-Fingerprint, X-Echos-Nonce, X-Echos-Signature); the relay validates the signature against the public key on file for that fingerprint, rejects any nonce older than a default 60s TTL or already consumed, and only the fingerprint that owns the resource (the mailbox owner for GET /mailbox, the blob's recipient for GET /blob) can authenticate successfully.
- On-disk session layouts match what was observed on this machine: Claude per-cwd-encoded project directories containing <uuid>.jsonl (plus an optional <uuid>/subagents/ subtree), and Codex sessions under ~/.codex/sessions/ indexed by ~/.codex/session_index.jsonl entries of {id, thread_name, updated_at}.
- The relay is a separate single Go binary; in development it runs on localhost:8080. Clients resolve the relay URL in this priority order: the ECHOS_RELAY environment variable if set, else the value in ~/.config/echos/config.json if present, else an implicit default of http://localhost:8080 — no interactive prompt is ever shown to configure it.
- echo-id fingerprints are collision-resistant enough for manual out-of-band exchange, and the human-readable name is purely a local alias stored in friends.json.
- Recipients run echos open inside their own checkout of the project (or pass --dir) because Claude Code resume is bound to the encoded project path.
- Codex sessions are resumed via codex resume <id> (mirroring claude --resume <id>), consistent with the Codex CLI's session_index.jsonl id field.
- Each tool adapter defines and validates its own set of safe, adapter-owned relative destination paths (e.g. Claude's <uuid>.jsonl and <uuid>/subagents/*, Codex's session rollout file); on unpack and before install, echos rejects any packaged file path that escapes the adapter's expected layout (absolute paths, .., symlinks, or paths outside the allowed prefixes), refusing to install rather than writing outside the intended destination.
- Packaged session files are untrusted input: the envelope may contain arbitrary relative paths, so `open`/Install must sanitize entries (no absolute paths, no `..` escape) and confine writes to adapter-owned locations before touching the filesystem; the adapter, not the archive, decides the destination layout.
- If `echos id` (or any command that lazily creates an identity) successfully generates and persists the local ed25519 key but the subsequent POST /keys publication to the relay fails (e.g. relay unreachable or returns an error), identity creation still succeeds and the command exits 0, printing the echo-id together with a warning that key publication failed and instructing the user to re-run the idempotent `echos id` once the relay is reachable to retry publication; friends cannot resolve this echo-id via GET /keys/{fpr} until publication succeeds, but this does not block local-only usage such as `echos sessions` or `echos open`.
- The optional `--key` flag for identity creation accepts only unencrypted (no passphrase) ed25519 SSH private keys at the given path, read directly via golang.org/x/crypto/ssh; per the no-interactivity rule, echos never prompts for a passphrase — if the key file is passphrase-protected, echos attempts ssh-agent (when SSH_AUTH_SOCK is set) as a fallback signer and otherwise fails fast with an error naming the key path and instructing the user to supply an unencrypted key or load it into ssh-agent; non-ed25519 key types (rsa, ecdsa, etc.) are rejected with an explicit unsupported-key-type error since only ed25519 is supported by the age/agessh signing path.
- `echos inbox`'s per-item fields beyond relay metadata (from_fingerprint, from_name, tool) require decrypting each pending blob: because the zero-knowledge relay's GET /mailbox/{fpr} response carries only {id, size, received_at, expires_at} and sender identity/tool live inside the age ciphertext's manifest.json, `echos inbox` transparently fetches each blob via GET /blob/{id} (authenticated with the same challenge-signature flow as any other read) and age-decrypts it with the local identity's private key to populate from_fingerprint and tool, resolving from_name via a friends.json lookup on that fingerprint; this decryption is solely for listing display, is independent of and does not perform the sender-signature verification, unknown-sender rejection, or file installation that `echos open` performs, and every pending item must be individually decryptable by the local identity (since each was encrypted to it) for `echos inbox` to succeed without error.

## Clarifications
- None

## Project context
- Executor context: context/executor-context.md
- Accepted memory context: context/memory-context.md

## Accepted memory

Memory context:
- context/memory-context.md

Selected memory:
- total: 0
- fresh: 0
- stale: 0
- unknown: 0

Items:
- none

Rules:
- Accepted memory is context, not semantic truth.
- Stale memory may be outdated; verify before using.
- Inspect current source files before relying on memory.
- Do not implement from memory alone.

## Instructions for future executor
- Follow the approved contract.
- Do not implement out-of-scope work.
- Search before creating new code.
- Prefer existing exported functions and types when applicable.
- If the contract is ambiguous, stop and request clarification.
- Use the listed validation commands as expected checks.
- Pactum gate can run approved validation commands after execution.

## House style
- Match the surrounding code: idiom, naming, comment density.
- Comment only where the code is not self-explanatory; do not narrate the obvious.
- Search for and reuse existing helpers before writing new ones.
- Keep the diff small and focused: change only what the contract requires.
- Simplicity first: no enterprise patterns for simple problems, question every new abstraction, no premature generalization or optimization.
- Over-engineering DON'Ts: wrappers that add nothing, factories or abstractions for a single case, unused extension points, dual implementations where the old path has no callers, silent fallbacks that hide failures.
- No dead code, no commented-out code, no unused parameters.
- Handle errors per the project's existing convention; no silent failures.
- Tests verify behavior, not implementation details, and cover error paths.
- Fake-test DON'Ts: always-pass tests, hardcoded-value checks, assertions on mock behavior instead of the code under test, ignored errors, commented-out cases.
