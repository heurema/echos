# Memory Candidate

## Run
- Run id: run_20260702_085315
- Source: deterministic

## Contract
- Goal: Deliver echos v0 per SPEC.md: an agent-first CLI that shares a running coding-agent session (Claude Code / Codex) with a friend end-to-end encrypted through an ephemeral zero-knowledge relay — one command to send, one to receive. Honor the two versioned contracts (Envelope v1, Relay API) and the core principles: agent-first / no interactivity, courier-not-translator (native transcript bytes), zero-knowledge relay, ephemerality.
- In scope:
  - Session discovery adapters exposing a common Session model: Claude Code (~/.claude/projects/<enc-cwd>/<uuid>.jsonl, including the optional <uuid>/subagents/ subtree) and Codex (~/.codex/session_index.jsonl + rollout files), surfaced by `echos sessions [--json]` newest-first with tool, id, project, title, updated.
  - Lazy identity: a fresh ed25519 key at ~/.config/echos/identity (0600), exposed by an idempotent `echos id`; optional reuse of an existing SSH key via --key; on identity creation the public key is published to the relay (POST /keys) so friends can resolve it by echo-id. Fingerprint derivation: fingerprint = lowercase hex-encoded SHA-256 digest of the ed25519 public key's SSH wire-format bytes (golang.org/x/crypto/ssh PublicKey.Marshal() output); echo-id is the first 20 hex characters (80 bits) of that fingerprint, and the relay's {fpr} path parameter is this same 20-character echo-id string — echo-id and fpr are identical values derived identically by every party, so a friend's independently-computed hash of the fetched key must equal the echo-id given at `friend add` time.
  - Envelope contract v1 (wire format, byte-exact): a 5-byte header consisting of the literal ASCII magic bytes "ECHO" (0x45 0x43 0x48 0x4F) followed by a single version byte 0x01, then immediately (no length prefix) the age ciphertext produced by encrypting to the recipient's ed25519/SSH public key via filippo.io/age/agessh; the age plaintext is a gzip'd tar (tar.gz) containing exactly: manifest.json, signature.sig, and the native session files at the tool adapter's declared relative paths. manifest.json schema: {"version":1, "tool":"claude"|"codex", "session_id":string, "project":string, "title":string, "sender_echo_id":string, "sender_fingerprint":string, "created_at":RFC3339 string, "files":[{"path":string, "size":int, "sha256":hex string}, ...]} — files[] lists every native file in the archive with its relative path (as used by that tool adapter's Install) and a SHA-256 of its exact bytes. Signing input and domain: signature.sig contains a golang.org/x/crypto/ssh Signer.Sign() signature (marshaled via ssh.Marshal) computed over the raw serialized bytes of manifest.json itself (not the native files directly — their integrity is bound transitively via the files[].sha256 entries manifest.json contains); verification recomputes SHA-256 over each unpacked native file and confirms it matches the corresponding files[].sha256 entry, then verifies signature.sig against manifest.json's bytes using the sender's public key from friends.json, rejecting the envelope if either check fails. The sender identity (sender_echo_id/sender_fingerprint in manifest.json) and signature.sig live INSIDE the age ciphertext so the relay never sees them.
  - Zero-knowledge relay binary (cmd/echos-relay) implementing Relay API v1 over JSON (except raw blob bodies) with these exact request/response shapes: POST /keys — request body {"fingerprint":string, "public_key":base64 string of the SSH wire-format public key}; response 201 (or 200 if the fingerprint is already registered with an identical key) {"fingerprint":string, "created":bool}; 409 if the fingerprint already exists with a different key. GET /keys/{fpr} — response 200 {"fingerprint":string, "public_key":base64 string}; 404 {"error":string} if unknown. GET /challenge?fpr={fpr} — response 200 {"nonce":base64 string of 32 random bytes, "expires_at":RFC3339 string} with a default 60s TTL from issuance; 404 if fpr is unknown. POST /mailbox/{fpr} — request body is the raw envelope bytes (application/octet-stream); response 201 {"id":string blob id, "ttl":int seconds, "expires_at":RFC3339 string}; 413 {"error":string} if the body exceeds the configured max size (persists nothing); 429 {"error":string} if the per-source-IP rate limit is exceeded. GET /mailbox/{fpr} — requires challenge-signature auth headers (X-Echos-Fingerprint, X-Echos-Nonce, X-Echos-Signature); response 200 with a JSON array of {"id":string, "size":int, "received_at":RFC3339 string, "expires_at":RFC3339 string} metadata (ciphertext itself is fetched separately via GET /blob/{id}); 401 {"error":string} on missing/invalid/expired/replayed auth. GET /blob/{id} — requires the same challenge-signature auth headers, authenticated as the blob's recipient fingerprint; response 200 application/octet-stream raw envelope bytes; 401 {"error":string} on auth failure; 404 if the id is unknown; 410 {"error":string} if the blob has expired. Challenge-signature auth required on both reads (GET /mailbox and GET /blob), with GET /challenge issuing a short-lived (default 60s TTL) single-use nonce that the client signs to authenticate those reads, rejected with 401 once expired or already consumed by a prior request; bbolt-backed store with a TTL sweeper, default TTL 24h; POST /mailbox/{fpr} rejects blobs above a configurable max size (default 25MiB) with HTTP 413, and POST /keys and POST /mailbox/{fpr} are rate-limited per source IP (default 10 requests/minute) returning HTTP 429 when exceeded, both limits configurable via relay startup flags/env vars.
  - Friends: `echos friend add <name> <echo-id>` fetches the pubkey from the relay and verifies it hashes to the given fingerprint; friends stored in ~/.config/echos/friends.json as local name->identity aliases.
  - Core verbs: `echos send <friend> [session-id]` (default session = latest in the current cwd project) uploads an envelope addressed to the friend's fingerprint and prints a success line with TTL; `echos inbox` lists pending items by fetching each pending blob's relay metadata (id, size, received_at, expires_at) via GET /mailbox/{fpr} and then, since sender identity and tool are not known to the relay, age-decrypting each blob via GET /blob/{id} with the local identity's private key to read manifest.json for from_fingerprint, tool, and (via a friends.json lookup) from_name — this decrypt-for-listing does not verify the sender's signature or install any files, it is a read-only enrichment of the listing, distinct from `echos open`'s full verify+install; `echos open [id] [--dir]` (default = newest inbox item, dir=cwd) decrypts, verifies the sender against the address book, installs native files, and prints the resume command.
  - Behavior contract enforced across all commands: never read stdin / no interactivity, --json with a stable schema on every command, exit codes 0 ok / 1 error / 2 needs-clarification, every error prints a ready-to-run next command, no exec by default (open prints the resume command; --resume opts in), and open refuses a sender not in friends unless --allow-unknown.
  - open degradation matrix: same tool -> install into the right location plus resume command; a different or unknown tool -> save the files and print their path; open never dead-ends on an unknown tool.
  - Tool-adapter seam (Discover/Package/Install/ResumeCommand) that isolates all tool specifics so a new agent is one adapter and the wire format is untouched.
- Out of scope:
  - Normalizing or converting transcripts between tools — resumability requires the native jsonl byte-for-byte.
  - Live session synchronization — echos shares only a snapshot captured at send time.
  - Transferring code or repository state (a later manifest git_rev + divergence warning is explicitly deferred).
  - Accounts, or long-term retention of message/session content on the relay — this does not include the public-key directory (POST/GET /keys) needed for echo-id resolution or the TTL-bound mailbox/blob store, both of which are in scope-in.
  - Second-ring features (send --link secret links, --scrub, --once, inbox --watch long-poll, friend list/rm, markdown exporters) — additive and out of the v0 core.
- Acceptance criteria:
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
- Validation commands:
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

## Outcome
- Gate status: needs_review
- Review status: approved
- Execution exit code: 0
- Validation passed: true
- Changes need review: true

## Changes
- Changed files: none
- New files:
  - cmd/echos-relay/main.go
  - cmd/echos/app.go
  - cmd/echos/cli.go
  - cmd/echos/cli_behavior_test.go
  - cmd/echos/cmd_friend.go
  - cmd/echos/cmd_id.go
  - cmd/echos/cmd_inbox.go
  - cmd/echos/cmd_open.go
  - cmd/echos/cmd_send.go
  - cmd/echos/cmd_sessions.go
  - cmd/echos/e2e_test.go
  - cmd/echos/identity_lifecycle_test.go
  - cmd/echos/json_schema_test.go
  - cmd/echos/key_friend_test.go
  - cmd/echos/main.go
  - cmd/echos/open_degradation_test.go
  - cmd/echos/open_unknown_sender_test.go
  - cmd/echos/send_default_test.go
  - cmd/echos/testutil_test.go
  - go.mod
  - go.sum
  - internal/envelope/archive_safety_test.go
  - internal/envelope/envelope.go
  - internal/envelope/envelope_test.go
  - internal/envelope/manifest.go
  - internal/identity/config.go
  - internal/identity/friends.go
  - internal/identity/identity.go
  - internal/identity/identity_test.go
  - internal/relay/client.go
  - internal/relay/client_test.go
  - internal/relayserver/challenge.go
  - internal/relayserver/ratelimit.go
  - internal/relayserver/server.go
  - internal/relayserver/server_test.go
  - internal/relayserver/store.go
  - internal/session/claude.go
  - internal/session/codex.go
  - internal/session/session.go
  - internal/session/session_test.go
- Missing files: none

## Clarifications
- None

## Review Decisions
- f_001 [medium] resolved internal/relayserver/challenge.go:44: The relay's challenge-nonce store grows without bound: issued nonces are only removed when consumed, expired-but-unconsumed nonces are never swept, and GET /challenge is not rate-limited, so a client can inflate relay memory indefinitely.
  Resolution: Fixed: ChallengeStore now sweeps expired records on each Issue (sweepLocked under the mutex), so issued-but-never-consumed nonces cannot grow the store beyond the TTL window (challenge.go).
- f_002 [low] open internal/relayserver/ratelimit.go:35: The per-source-IP rate limiter map is never evicted, so requests from an unbounded set of distinct source IPs grow relay memory without bound.
- f_003 [low] resolved internal/envelope/envelope.go:280: VerifyFiles only iterates manifest.Files, so an unpacked archive entry that is absent from the manifest is installed without any SHA-256 verification, contrary to the contract's 'recomputes SHA-256 over each unpacked native file' requirement.
  Resolution: Fixed: VerifyFiles now fails closed on any unpacked entry absent from the signed manifest (envelope.go), binding the full extracted file set to manifest.json so unmanifested files can never be installed unverified.
- f_004 [low] resolved cmd/echos/cmd_send.go:34: The 'no friend' error hint in send prints an echo-id with a spurious 'echo:' prefix, producing an incorrect next command that contradicts the documented echo-id format and the agent-first 'ready-to-run next command' contract.
  Resolution: Fixed: removed the spurious 'echo:' prefix from send's no-friend hint (cmd_send.go:34); it now emits 'echos friend add <friend> <their-echo-id>', matching the bare echo-id that 'echos id' prints and that friend add's hash comparison expects.
- f_005 [medium] open internal/identity/identity.go:112: The `--key` external-SSH-key identity path (createFromExternalKey and its branches: unencrypted-ed25519 reuse, non-ed25519 rejection, passphrase→ssh-agent fallback, and the agent-backed load path) has no test coverage; all tests exercise only Ensure(dir, "") / `echos id` with a freshly generated key.
- f_006 [low] open cmd/echos/cmd_open.go:145: The `echos open --resume` execution path (the `if c.Resume` branch and runResume) is never exercised; no test passes --resume, so the opt-in exec behavior mandated by the behavior contract is untested.
- f_007 [low] open cmd/echos/app.go:45: The key-publication-failure degradation branch in ensureIdentity (identity still created, warning printed, exit 0 when the relay POST /keys fails) is untested; every test starts a relay before creating an identity.
- f_008 [low] open internal/envelope/envelope.go:279: envelope.VerifyFiles is only ever run on matching input; the size/checksum-mismatch rejection branch that binds native-file bytes to the signed manifest is untested.
- f_009 [low] open internal/session/codex.go:84: rolloutsByID's WalkDir callback returns nil on every error, so the helper never returns a non-nil error; the os.IsNotExist(err) recovery in Discover (and the error returns in rolloutsByID and Package) are unreachable dead code.
- f_010 [low] open SPEC.md:159: SPEC.md §4 documents the Envelope v1 wire format inconsistently with the shipped/contracted implementation: it states the header magic is "ECHS" and shows a manifest.json schema (echos, project_hint, sender{echo_id,pubkey}, files[{path,role}]) that both differ from what ships (magic "ECHO"; manifest fields version/tool/session_id/project/title/sender_echo_id/sender_fingerprint/created_at/files[{path,size,sha256}]). As the repo's only human-readable spec for a versioned wire contract, it would mislead a reimplementer.
- f_011 [low] open cmd/echos/cmd_send.go:34: The echos CLI documents the echo-id in two conflicting forms: `echos id` prints a bare 20-hex-char id and the `friend add` help says 'as printed by echos id', but the `echos send` failure hint instructs the user to run `echos friend add <friend> echo:<their-echo-id>` with an `echo:` prefix. A user copying the hinted form passes a prefixed value that friend add's relay lookup and hash comparison will not match.
- Proposal summary: pending=0 accepted=11 rejected=0

## Reusable Project Knowledge
- scope: in scope: Session discovery adapters exposing a common Session model: Claude Code (~/.claude/projects/<enc-cwd>/<uuid>.jsonl, including the optional <uuid>/subagents/ subtree) and Codex (~/.codex/session_index.jsonl + rollout files), surfaced by `echos sessions [--json]` newest-first with tool, id, project, title, updated.
- scope: in scope: Lazy identity: a fresh ed25519 key at ~/.config/echos/identity (0600), exposed by an idempotent `echos id`; optional reuse of an existing SSH key via --key; on identity creation the public key is published to the relay (POST /keys) so friends can resolve it by echo-id. Fingerprint derivation: fingerprint = lowercase hex-encoded SHA-256 digest of the ed25519 public key's SSH wire-format bytes (golang.org/x/crypto/ssh PublicKey.Marshal() output); echo-id is the first 20 hex characters (80 bits) of that fingerprint, and the relay's {fpr} path parameter is this same 20-character echo-id string — echo-id and fpr are identical values derived identically by every party, so a friend's independently-computed hash of the fetched key must equal the echo-id given at `friend add` time.
- scope: in scope: Envelope contract v1 (wire format, byte-exact): a 5-byte header consisting of the literal ASCII magic bytes "ECHO" (0x45 0x43 0x48 0x4F) followed by a single version byte 0x01, then immediately (no length prefix) the age ciphertext produced by encrypting to the recipient's ed25519/SSH public key via filippo.io/age/agessh; the age plaintext is a gzip'd tar (tar.gz) containing exactly: manifest.json, signature.sig, and the native session files at the tool adapter's declared relative paths. manifest.json schema: {"version":1, "tool":"claude"|"codex", "session_id":string, "project":string, "title":string, "sender_echo_id":string, "sender_fingerprint":string, "created_at":RFC3339 string, "files":[{"path":string, "size":int, "sha256":hex string}, ...]} — files[] lists every native file in the archive with its relative path (as used by that tool adapter's Install) and a SHA-256 of its exact bytes. Signing input and domain: signature.sig contains a golang.org/x/crypto/ssh Signer.Sign() signature (marshaled via ssh.Marshal) computed over the raw serialized bytes of manifest.json itself (not the native files directly — their integrity is bound transitively via the files[].sha256 entries manifest.json contains); verification recomputes SHA-256 over each unpacked native file and confirms it matches the corresponding files[].sha256 entry, then verifies signature.sig against manifest.json's bytes using the sender's public key from friends.json, rejecting the envelope if either check fails. The sender identity (sender_echo_id/sender_fingerprint in manifest.json) and signature.sig live INSIDE the age ciphertext so the relay never sees them.
- scope: in scope: Zero-knowledge relay binary (cmd/echos-relay) implementing Relay API v1 over JSON (except raw blob bodies) with these exact request/response shapes: POST /keys — request body {"fingerprint":string, "public_key":base64 string of the SSH wire-format public key}; response 201 (or 200 if the fingerprint is already registered with an identical key) {"fingerprint":string, "created":bool}; 409 if the fingerprint already exists with a different key. GET /keys/{fpr} — response 200 {"fingerprint":string, "public_key":base64 string}; 404 {"error":string} if unknown. GET /challenge?fpr={fpr} — response 200 {"nonce":base64 string of 32 random bytes, "expires_at":RFC3339 string} with a default 60s TTL from issuance; 404 if fpr is unknown. POST /mailbox/{fpr} — request body is the raw envelope bytes (application/octet-stream); response 201 {"id":string blob id, "ttl":int seconds, "expires_at":RFC3339 string}; 413 {"error":string} if the body exceeds the configured max size (persists nothing); 429 {"error":string} if the per-source-IP rate limit is exceeded. GET /mailbox/{fpr} — requires challenge-signature auth headers (X-Echos-Fingerprint, X-Echos-Nonce, X-Echos-Signature); response 200 with a JSON array of {"id":string, "size":int, "received_at":RFC3339 string, "expires_at":RFC3339 string} metadata (ciphertext itself is fetched separately via GET /blob/{id}); 401 {"error":string} on missing/invalid/expired/replayed auth. GET /blob/{id} — requires the same challenge-signature auth headers, authenticated as the blob's recipient fingerprint; response 200 application/octet-stream raw envelope bytes; 401 {"error":string} on auth failure; 404 if the id is unknown; 410 {"error":string} if the blob has expired. Challenge-signature auth required on both reads (GET /mailbox and GET /blob), with GET /challenge issuing a short-lived (default 60s TTL) single-use nonce that the client signs to authenticate those reads, rejected with 401 once expired or already consumed by a prior request; bbolt-backed store with a TTL sweeper, default TTL 24h; POST /mailbox/{fpr} rejects blobs above a configurable max size (default 25MiB) with HTTP 413, and POST /keys and POST /mailbox/{fpr} are rate-limited per source IP (default 10 requests/minute) returning HTTP 429 when exceeded, both limits configurable via relay startup flags/env vars.
- scope: in scope: Friends: `echos friend add <name> <echo-id>` fetches the pubkey from the relay and verifies it hashes to the given fingerprint; friends stored in ~/.config/echos/friends.json as local name->identity aliases.
- scope: in scope: Core verbs: `echos send <friend> [session-id]` (default session = latest in the current cwd project) uploads an envelope addressed to the friend's fingerprint and prints a success line with TTL; `echos inbox` lists pending items by fetching each pending blob's relay metadata (id, size, received_at, expires_at) via GET /mailbox/{fpr} and then, since sender identity and tool are not known to the relay, age-decrypting each blob via GET /blob/{id} with the local identity's private key to read manifest.json for from_fingerprint, tool, and (via a friends.json lookup) from_name — this decrypt-for-listing does not verify the sender's signature or install any files, it is a read-only enrichment of the listing, distinct from `echos open`'s full verify+install; `echos open [id] [--dir]` (default = newest inbox item, dir=cwd) decrypts, verifies the sender against the address book, installs native files, and prints the resume command.
- scope: in scope: Behavior contract enforced across all commands: never read stdin / no interactivity, --json with a stable schema on every command, exit codes 0 ok / 1 error / 2 needs-clarification, every error prints a ready-to-run next command, no exec by default (open prints the resume command; --resume opts in), and open refuses a sender not in friends unless --allow-unknown.
- scope: in scope: open degradation matrix: same tool -> install into the right location plus resume command; a different or unknown tool -> save the files and print their path; open never dead-ends on an unknown tool.
- scope: in scope: Tool-adapter seam (Discover/Package/Install/ResumeCommand) that isolates all tool specifics so a new agent is one adapter and the wire format is untouched.
- scope: out of scope: Normalizing or converting transcripts between tools — resumability requires the native jsonl byte-for-byte.
- scope: out of scope: Live session synchronization — echos shares only a snapshot captured at send time.
- scope: out of scope: Transferring code or repository state (a later manifest git_rev + divergence warning is explicitly deferred).
- scope: out of scope: Accounts, or long-term retention of message/session content on the relay — this does not include the public-key directory (POST/GET /keys) needed for echo-id resolution or the TTL-bound mailbox/blob store, both of which are in scope-in.
- scope: out of scope: Second-ring features (send --link secret links, --scrub, --once, inbox --watch long-poll, friend list/rm, markdown exporters) — additive and out of the v0 core.
- review_resolution: f_001 resolved: The relay's challenge-nonce store grows without bound: issued nonces are only removed when consumed, expired-but-unconsumed nonces are never swept, and GET /challenge is not rate-limited, so a client can inflate relay memory indefinitely.; resolution: Fixed: ChallengeStore now sweeps expired records on each Issue (sweepLocked under the mutex), so issued-but-never-consumed nonces cannot grow the store beyond the TTL window (challenge.go).
- review_resolution: f_003 resolved: VerifyFiles only iterates manifest.Files, so an unpacked archive entry that is absent from the manifest is installed without any SHA-256 verification, contrary to the contract's 'recomputes SHA-256 over each unpacked native file' requirement.; resolution: Fixed: VerifyFiles now fails closed on any unpacked entry absent from the signed manifest (envelope.go), binding the full extracted file set to manifest.json so unmanifested files can never be installed unverified.
- review_resolution: f_004 resolved: The 'no friend' error hint in send prints an echo-id with a spurious 'echo:' prefix, producing an incorrect next command that contradicts the documented echo-id format and the agent-first 'ready-to-run next command' contract.; resolution: Fixed: removed the spurious 'echo:' prefix from send's no-friend hint (cmd_send.go:34); it now emits 'echos friend add <friend> <their-echo-id>', matching the bare echo-id that 'echos id' prints and that friend add's hash comparison expects.
- review_resolution: proposal p_001 accepted as f_001
- review_resolution: proposal p_002 accepted as f_002
- review_resolution: proposal p_003 accepted as f_003
- review_resolution: proposal p_004 accepted as f_004
- review_resolution: proposal p_005 accepted as f_005
- review_resolution: proposal p_006 accepted as f_006
- review_resolution: proposal p_007 accepted as f_007
- review_resolution: proposal p_008 accepted as f_008
- review_resolution: proposal p_009 accepted as f_009
- review_resolution: proposal p_010 accepted as f_010
- review_resolution: proposal p_011 accepted as f_011
- validation: go build ./... passed
- validation: go vet ./... passed
- validation: go test ./... passed
- validation: sh -c 'grep -rl "func TestEnvelopeRoundTrip(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestEnvelopeRoundTrip$" -v | grep -q -- "--- PASS: TestEnvelopeRoundTrip"' passed
- validation: sh -c 'grep -rl "func TestSubagentsSubtreeRoundTrip(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestSubagentsSubtreeRoundTrip$" -v | grep -q -- "--- PASS: TestSubagentsSubtreeRoundTrip"' passed
- validation: sh -c 'grep -rl "func TestRelayAuthRejectsUnsignedOrInvalidReads(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestRelayAuthRejectsUnsignedOrInvalidReads$" -v | grep -q -- "--- PASS: TestRelayAuthRejectsUnsignedOrInvalidReads"' passed
- validation: sh -c 'grep -rl "func TestEndToEndSendInboxOpen_Claude(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestEndToEndSendInboxOpen_Claude$" -v | grep -q -- "--- PASS: TestEndToEndSendInboxOpen_Claude"' passed
- validation: sh -c 'grep -rl "func TestEndToEndSendInboxOpen_Codex(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestEndToEndSendInboxOpen_Codex$" -v | grep -q -- "--- PASS: TestEndToEndSendInboxOpen_Codex"' passed
- validation: sh -c 'grep -rl "func TestOpenDegradationMatrix(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestOpenDegradationMatrix$" -v | grep -q -- "--- PASS: TestOpenDegradationMatrix"' passed
- validation: sh -c 'grep -rl "func TestOpenRejectsUnsafeArchivePaths(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestOpenRejectsUnsafeArchivePaths$" -v | grep -q -- "--- PASS: TestOpenRejectsUnsafeArchivePaths"' passed
- validation: sh -c 'grep -rl "func TestJSONOutputSchemas(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestJSONOutputSchemas$" -v | grep -q -- "--- PASS: TestJSONOutputSchemas"' passed
- validation: sh -c 'grep -rl "func TestKeyPublicationAndFriendResolution(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestKeyPublicationAndFriendResolution$" -v | grep -q -- "--- PASS: TestKeyPublicationAndFriendResolution"' passed
- validation: sh -c 'grep -rl "func TestSendDefaultsToCwdProjectLatestSession(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestSendDefaultsToCwdProjectLatestSession$" -v | grep -q -- "--- PASS: TestSendDefaultsToCwdProjectLatestSession"' passed
- validation: sh -c 'grep -rl "func TestEnvelopeHeaderAndInternalSignature(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestEnvelopeHeaderAndInternalSignature$" -v | grep -q -- "--- PASS: TestEnvelopeHeaderAndInternalSignature"' passed
- validation: sh -c 'grep -rl "func TestSessionsDiscovery(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestSessionsDiscovery$" -v | grep -q -- "--- PASS: TestSessionsDiscovery"' passed
- validation: sh -c 'grep -rl "func TestIdentityLifecycle(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestIdentityLifecycle$" -v | grep -q -- "--- PASS: TestIdentityLifecycle"' passed
- validation: sh -c 'grep -rl "func TestOpenRejectsUnknownSender(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestOpenRejectsUnknownSender$" -v | grep -q -- "--- PASS: TestOpenRejectsUnknownSender"' passed
- validation: sh -c 'grep -rl "func TestRelayZeroKnowledgeStorageAndExpiry(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestRelayZeroKnowledgeStorageAndExpiry$" -v | grep -q -- "--- PASS: TestRelayZeroKnowledgeStorageAndExpiry"' passed
- validation: sh -c 'grep -rl "func TestCLIBehaviorContract(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestCLIBehaviorContract$" -v | grep -q -- "--- PASS: TestCLIBehaviorContract"' passed
- validation: sh -c 'grep -rl "func TestChallengeNonceExpiryAndReplay(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestChallengeNonceExpiryAndReplay$" -v | grep -q -- "--- PASS: TestChallengeNonceExpiryAndReplay"' passed
- validation: sh -c 'grep -rl "func TestRelayLimits(t \*testing.T)" --include="*_test.go" . | grep -q . && go test ./... -run "^TestRelayLimits$" -v | grep -q -- "--- PASS: TestRelayLimits"' passed

## Artifacts
- Contract: contract/contract.json
- Gate report: gate/gate-report.json
- Review: review/review.json
- Findings: review/findings.jsonl
- Resolutions: review/resolutions.jsonl
- Proposals: review/proposals.jsonl
- Proposal decisions: review/proposal-decisions.jsonl
