# Contract Review: Validation soundness

You are reviewing a software change contract through the **validation-soundness** lens.

Review the contract fields below using only your assigned lens checklist.
Do not flag issues that belong to other lenses.

## Contract

**Goal**: Pay down the advisory tech-debt surfaced by run_20260702_085315's code review: correct SPEC.md so it matches the shipped Envelope v1 / Relay API wire format, bound the relay's per-source-IP rate-limiter memory, remove unreachable dead code in the Codex adapter, and add tests for the currently-untested identity/open/publish/verify branches — all WITHOUT changing the shipped wire contract or CLI behavior (docs and tests are brought in line with the code, not the reverse).

**Scope in**:
  - Update SPEC.md sections 4 (Envelope) and 5 (Relay API) to match the shipped implementation: the 5-byte header of ASCII magic "ECHO" + version byte 0x01 (not "ECHS"); the real manifest.json schema {version,tool,session_id,project,title,sender_echo_id,sender_fingerprint,created_at,files[{path,size,sha256}]}; signature.sig computed over manifest.json's raw bytes, with the files[].sha256 transitive-binding explained; and the actual relay endpoints/shapes including the exact `GET /challenge?fpr={fpr}` endpoint, each of the three auth headers (X-Echos-Fingerprint, X-Echos-Nonce, X-Echos-Signature) named individually, and each of the five relay status codes attached to the endpoint(s) that produce it (201 Created on POST /keys and POST /mailbox, 401 Unauthorized on failed challenge auth, 410 Gone on expired GET /blob, 413 Request Entity Too Large on oversized POST /mailbox, 429 Too Many Requests on rate-limited POST /mailbox). Also reconcile any remaining stale mentions (e.g. echo-id = first 20 hex of the SHA-256 key fingerprint; kong not stdlib flag).
  - Bound the relay per-source-IP rate limiter (internal/relayserver/ratelimit.go) memory footprint with two complementary mechanisms: (a) entries that have been idle (no request from that source IP) for longer than a defined idle TTL are evicted — via the same periodic-sweep pattern as the already-fixed challenge-nonce sweep (or lazy eviction on access, or both); and (b) a hard maximum entry-count cap is enforced on the limiter map — when adding a new source IP's entry would exceed the cap, the least-recently-used entry is evicted to make room. Together these ensure the map's total entry count is truly bounded at all times: bounded by the idle TTL under normal turnover, and bounded by the hard cap even under sustained traffic from more distinct, simultaneously-active (non-idle) source IPs than the cap, regardless of how many requests any single IP sends. This is an internal memory-management mechanism of the limiter's bookkeeping map, not a change to the documented Relay API request/response shapes, endpoints, or status codes: it only alters which source IP's rate-limit counter is evicted when the number of distinct, concurrently-active source IPs exceeds the cap, and has no observable effect on traffic patterns that stay within the cap.
  - Remove the unreachable dead code in internal/session/codex.go: rolloutsByID's WalkDir callback currently returns nil on every error, making the os.IsNotExist(err) recovery in Discover and the error returns in rolloutsByID/Package unreachable — delete the dead branches (the unreachable os.IsNotExist(err) recovery in Discover and the unreachable error returns in rolloutsByID/Package) rather than propagating WalkDir errors, since propagating errors could change Discover/Package's observable behavior (exit codes, --json output, error-next-command), which scope.out forbids changing.
  - Add regression tests for the untested branches: the --key external-SSH-key identity path [f_005], covered by three separate named tests — one exercising unencrypted-ed25519 key reuse, one exercising rejection of a non-ed25519 key, and one exercising ssh-agent fallback; the `echos open --resume` opt-in exec path [f_006]; the identity key-publication-failure degradation (identity still created, warning printed, exit 0 when relay POST /keys fails) [f_007]; and envelope VerifyFiles' size/checksum-mismatch rejection branch [f_008].

**Scope out**:
  - Any change to the Envelope v1 wire format or the Relay API's documented request/response shapes, endpoints, or status codes — SPEC.md is corrected to describe the code, the code is not changed to match old docs. This does not prohibit the scope.in-required internal rate-limiter LRU/idle-TTL eviction mechanism, which manages the limiter's in-memory bookkeeping map and does not alter any documented endpoint, request/response shape, or status code, nor any behavior observed within the configured entry-count cap.
  - Any change to the CLI command surface, flags, output, or the behavior contract (exit codes, --json, no-stdin, error-next-command).
  - New product features or any second-ring item (send --link, --scrub, --once, inbox --watch, friend list/rm, exporters).
  - Re-architecting the relay store, identity, or adapters beyond the localized fixes above.

**Acceptance criteria**:
  - SPEC.md sections 4 (Envelope) and 5 (Relay API) are corrected to match the shipped code: the header is the 5-byte "ECHO" magic + version byte 0x01; the manifest.json schema is documented with its full field set {version, tool, session_id, project, title, sender_echo_id, sender_fingerprint, created_at, files[{path,size,sha256}]}; signature.sig is documented as signing manifest.json's raw bytes with files bound transitively via files[].sha256; and the Relay API documents GET /challenge?fpr={fpr}, the three auth headers (X-Echos-Fingerprint/Nonce/Signature), and the real status codes (201/401/410/413/429). SPEC.md no longer references the old "ECHS" magic, the stale manifest fields (project_hint, "echos":1), or a stdlib flag CLI. Representative facts are spot-checked by grep at gate time; exhaustive per-token grepping is explicitly not required.
  - The relay per-source-IP rate limiter bounds memory two complementary ways: (1) it evicts entries idle longer than a defined idle TTL — covered by TestRateLimiterEvictsIdleEntries, which uses the injectable clock to advance past the idle TTL and asserts the map no longer retains the idle entries / stays at a bounded size, deterministically (no real sleeps); and (2) it enforces a hard maximum entry-count cap via least-recently-used eviction, so the map's total entry count never exceeds that cap even when more distinct source IPs than the cap are simultaneously active and none is idle — covered by TestRateLimiterEnforcesCapacityBound, which drives requests from more distinct, concurrently-active source IPs than the configured cap and asserts both (a) the map's entry count never exceeds the cap at any point, and (b) the eviction order is actually least-recently-used and not merely bounded: the test touches a specific existing entry (e.g. by sending it a request) to make it the most-recently-used among the first cap entries immediately before inserting one more distinct source IP beyond the cap, then asserts that the untouched, least-recently-used entry is the one evicted (its state is gone / reset) while the touched entry's state survives.
  - internal/session/codex.go has no unreachable error-handling dead code: the unreachable os.IsNotExist(err) recovery branch in Discover and the unreachable error-return branches in rolloutsByID/Package are deleted (WalkDir errors continue to be swallowed rather than propagated, per scope.in). Verified by go vet ./... passing and the code review confirming no unreachable branches remain; Discover/Package behavior for the covered on-disk layouts is unchanged.
  - New named tests exist, actually execute, and pass, covering the previously-untested branches: three distinct tests for the --key external-SSH-key identity path — TestIdentityExternalKeyUnencryptedReuse (unencrypted-ed25519 key reuse), TestIdentityExternalKeyRejectsNonEd25519 (non-ed25519 key rejection), and TestIdentityExternalKeySSHAgentFallback (ssh-agent fallback) — plus TestOpenResumeExec for `echos open --resume` exec, TestIdentityPublishFailureDegrades for identity publish-failure degradation, and TestEnvelopeVerifyFilesRejectsMismatch for envelope VerifyFiles mismatch rejection. Each test is confirmed to exist via grep of its `func <TestName>(t *testing.T)` signature, and confirmed passing by running `go test ./... -run '^<TestName>$' -v` piped into a grep for a `--- PASS: <TestName>` line naming that exact test.
  - No regression: go build ./..., go vet ./..., and go test ./... all pass, and all 18 acceptance tests named in run_20260702_085315's contract still pass: TestSessionsDiscovery, TestIdentityLifecycle, TestEnvelopeRoundTrip, TestSubagentsSubtreeRoundTrip, TestRelayAuthRejectsUnsignedOrInvalidReads, TestEndToEndSendInboxOpen_Claude, TestEndToEndSendInboxOpen_Codex, TestOpenDegradationMatrix, TestOpenRejectsUnsafeArchivePaths, TestJSONOutputSchemas, TestKeyPublicationAndFriendResolution, TestSendDefaultsToCwdProjectLatestSession, TestEnvelopeHeaderAndInternalSignature, TestOpenRejectsUnknownSender, TestRelayZeroKnowledgeStorageAndExpiry, TestCLIBehaviorContract, TestChallengeNonceExpiryAndReplay, TestRelayLimits (the wire format and CLI behavior are unchanged).

**Validation commands**:
  - go build ./...
  - go vet ./...
  - go test ./...
  - grep -q '"ECHO"' SPEC.md
  - grep -L 'ECHS' SPEC.md
  - grep -L 'project_hint' SPEC.md
  - grep -q 'sender_echo_id' SPEC.md
  - grep -q 'sender_fingerprint' SPEC.md
  - grep -q 'created_at' SPEC.md
  - grep -q 'GET /challenge' SPEC.md
  - grep -q 'X-Echos-Nonce' SPEC.md
  - grep -q '413' SPEC.md
  - grep -q '410' SPEC.md
  - grep -rl "func TestRateLimiterEvictsIdleEntries(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestRateLimiterEvictsIdleEntries$' -v | grep -q -- '--- PASS: TestRateLimiterEvictsIdleEntries'
  - grep -rl "func TestRateLimiterEnforcesCapacityBound(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestRateLimiterEnforcesCapacityBound$' -v | grep -q -- '--- PASS: TestRateLimiterEnforcesCapacityBound'
  - grep -rl "func TestIdentityExternalKeyUnencryptedReuse(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestIdentityExternalKeyUnencryptedReuse$' -v | grep -q -- '--- PASS: TestIdentityExternalKeyUnencryptedReuse'
  - grep -rl "func TestIdentityExternalKeyRejectsNonEd25519(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestIdentityExternalKeyRejectsNonEd25519$' -v | grep -q -- '--- PASS: TestIdentityExternalKeyRejectsNonEd25519'
  - grep -rl "func TestIdentityExternalKeySSHAgentFallback(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestIdentityExternalKeySSHAgentFallback$' -v | grep -q -- '--- PASS: TestIdentityExternalKeySSHAgentFallback'
  - grep -rl "func TestOpenResumeExec(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestOpenResumeExec$' -v | grep -q -- '--- PASS: TestOpenResumeExec'
  - grep -rl "func TestIdentityPublishFailureDegrades(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestIdentityPublishFailureDegrades$' -v | grep -q -- '--- PASS: TestIdentityPublishFailureDegrades'
  - grep -rl "func TestEnvelopeVerifyFilesRejectsMismatch(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestEnvelopeVerifyFilesRejectsMismatch$' -v | grep -q -- '--- PASS: TestEnvelopeVerifyFilesRejectsMismatch'
  - grep -rl "func TestSessionsDiscovery(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestSessionsDiscovery$' -v | grep -q -- '--- PASS: TestSessionsDiscovery'
  - grep -rl "func TestIdentityLifecycle(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestIdentityLifecycle$' -v | grep -q -- '--- PASS: TestIdentityLifecycle'
  - grep -rl "func TestEnvelopeRoundTrip(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestEnvelopeRoundTrip$' -v | grep -q -- '--- PASS: TestEnvelopeRoundTrip'
  - grep -rl "func TestSubagentsSubtreeRoundTrip(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestSubagentsSubtreeRoundTrip$' -v | grep -q -- '--- PASS: TestSubagentsSubtreeRoundTrip'
  - grep -rl "func TestRelayAuthRejectsUnsignedOrInvalidReads(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestRelayAuthRejectsUnsignedOrInvalidReads$' -v | grep -q -- '--- PASS: TestRelayAuthRejectsUnsignedOrInvalidReads'
  - grep -rl "func TestEndToEndSendInboxOpen_Claude(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestEndToEndSendInboxOpen_Claude$' -v | grep -q -- '--- PASS: TestEndToEndSendInboxOpen_Claude'
  - grep -rl "func TestEndToEndSendInboxOpen_Codex(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestEndToEndSendInboxOpen_Codex$' -v | grep -q -- '--- PASS: TestEndToEndSendInboxOpen_Codex'
  - grep -rl "func TestOpenDegradationMatrix(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestOpenDegradationMatrix$' -v | grep -q -- '--- PASS: TestOpenDegradationMatrix'
  - grep -rl "func TestOpenRejectsUnsafeArchivePaths(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestOpenRejectsUnsafeArchivePaths$' -v | grep -q -- '--- PASS: TestOpenRejectsUnsafeArchivePaths'
  - grep -rl "func TestJSONOutputSchemas(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestJSONOutputSchemas$' -v | grep -q -- '--- PASS: TestJSONOutputSchemas'
  - grep -rl "func TestKeyPublicationAndFriendResolution(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestKeyPublicationAndFriendResolution$' -v | grep -q -- '--- PASS: TestKeyPublicationAndFriendResolution'
  - grep -rl "func TestSendDefaultsToCwdProjectLatestSession(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestSendDefaultsToCwdProjectLatestSession$' -v | grep -q -- '--- PASS: TestSendDefaultsToCwdProjectLatestSession'
  - grep -rl "func TestEnvelopeHeaderAndInternalSignature(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestEnvelopeHeaderAndInternalSignature$' -v | grep -q -- '--- PASS: TestEnvelopeHeaderAndInternalSignature'
  - grep -rl "func TestOpenRejectsUnknownSender(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestOpenRejectsUnknownSender$' -v | grep -q -- '--- PASS: TestOpenRejectsUnknownSender'
  - grep -rl "func TestRelayZeroKnowledgeStorageAndExpiry(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestRelayZeroKnowledgeStorageAndExpiry$' -v | grep -q -- '--- PASS: TestRelayZeroKnowledgeStorageAndExpiry'
  - grep -rl "func TestCLIBehaviorContract(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestCLIBehaviorContract$' -v | grep -q -- '--- PASS: TestCLIBehaviorContract'
  - grep -rl "func TestChallengeNonceExpiryAndReplay(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestChallengeNonceExpiryAndReplay$' -v | grep -q -- '--- PASS: TestChallengeNonceExpiryAndReplay'
  - grep -rl "func TestRelayLimits(t \*testing.T)" --include='*_test.go' .
  - go test ./... -run '^TestRelayLimits$' -v | grep -q -- '--- PASS: TestRelayLimits'

**Assumptions**:
  - Go 1.26 toolchain; the fix reuses the existing stack (filippo.io/age, golang.org/x/crypto/ssh, kong, go.etcd.io/bbolt, golang.org/x/time/rate) with no new dependencies.
  - The shipped code (post run_20260702_085315, HEAD 7df1d48) is the source of truth for the wire format and CLI behavior; SPEC.md and tests are reconciled to it.
  - The rate limiter's idle eviction can be exercised deterministically in tests via the injectable clock already used elsewhere (app.Now / time injection), without real sleeps. The idle TTL threshold used for eviction is an implementation-chosen constant (not user-configurable), documented in code comments; TestRateLimiterEvictsIdleEntries advances the injected clock past that TTL and asserts the map no longer retains the idle entries, rather than asserting a specific TTL value.
  - The rate limiter's maximum entry-count cap is likewise an implementation-chosen constant (not user-configurable), defined as a package-level constant in internal/relayserver/ratelimit.go and documented in a code comment. TestRateLimiterEnforcesCapacityBound is a white-box test in the same package and references that constant identifier directly (rather than hardcoding a duplicate literal value), so the test stays correct if the constant is retuned later. The test drives requests from more distinct, concurrently-active (non-idle) source IPs than the constant's value, deliberately touches one of the first-cap entries to make it most-recently-used immediately before the (cap+1)th distinct IP arrives, and then asserts both that the map's entry count never exceeds the cap at any point and that the specific untouched least-recently-used entry (not the touched one) is the entry evicted — this is the mechanism that provides a true, order-verified bound even when idle-TTL eviction alone would not (i.e. when no entries ever go idle).
  - Deleting the Codex WalkDir dead-code branches (rather than propagating WalkDir errors) does not change observed Discover/Package results for the on-disk layouts already covered by session tests, since no new error-propagation path is introduced.
  - --key and ssh-agent test paths are exercised by three separate test functions, each using a generated key written to a temp dir: TestIdentityExternalKeyUnencryptedReuse using an unencrypted ed25519 key, TestIdentityExternalKeyRejectsNonEd25519 using a generated non-ed25519 key (e.g. RSA) to assert rejection, and TestIdentityExternalKeySSHAgentFallback using a test double / in-process ssh-agent — none depending on the operator's real ~/.ssh or a live ssh-agent.
  - This contract's no-regression gate depends on run_20260702_085315's contract.json (checked in at .heurema/pactum/runs/run_20260702_085315/contract/contract.json) as the source of record for the 18 prior acceptance tests; those test names and their grep+go test validation commands are reproduced directly in this contract's acceptance_criteria and validation.commands so the executor need not read that file separately, but it remains the authoritative source if any discrepancy is suspected.

## Lens: Validation soundness

Checklist:
- Are validation.commands gate-runnable (no shell forms the gate cannot execute)?
- Are they non-vacuous: would they fail on wrong output?
- Are they self-consistent and not contradictory with the tests?

## Output

Report likely-real defects (recall-first), then gate on precision before marking blocking.
Use state=candidate with explicit uncertainty when you believe a finding is real but have not fully confirmed it.

State your analysis in prose. You MUST also include exactly one structured findings block:
The block is mandatory — even when you have no findings, emit `"findings": []`.

```json
{
  "schema": "pactum.contract_reviewer_result.v1alpha1",
  "findings": [
    {
      "message": "Describe the contract issue clearly.",
      "severity": "medium",
      "category": "quality",
      "blocking": true,
      "evidence": "Quote or cite the contract field that shows the issue.",
      "material_impact": "Concrete way this spec defect would make the implementation wrong, ambiguous, or stuck.",
      "fix_direction": "What the contract author should change to resolve this.",
      "uncertainty": "Any doubt about this finding — omit if confident.",
      "state": "candidate"
    }
  ]
}
```

Rules:
- Use severity: low, medium, high, critical.
- Use category: correctness, scope, quality, validation, process, other.
- Omit file and line (not applicable for contract review).
- Set state=candidate when likely real but not fully confirmed; set state=confirmed when certain.
- HARD RULE: blocking=true is allowed ONLY for a material spec defect that would make the implementation wrong, ambiguous, or stuck.
- Wording, style, naming, redundancy, and completeness/thoroughness preferences MUST be blocking=false (advisory).
- Every blocking finding MUST include a concrete material_impact explaining the implementation consequence.
- If you cannot state a concrete material_impact, mark the finding blocking=false (advisory).
- Set blocking=false for advisory issues.
- If no issues, say so clearly and emit the mandatory empty findings block.
