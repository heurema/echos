package envelope

// ManifestFile records one native file packaged inside the envelope: its
// relative path (as used by the tool adapter's Install) and a SHA-256 of
// its exact bytes.
type ManifestFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest describes the packaged session. It is signed by the sender and
// travels, together with the native files, inside the age ciphertext.
type Manifest struct {
	Version           int            `json:"version"`
	Tool              string         `json:"tool"`
	SessionID         string         `json:"session_id"`
	Project           string         `json:"project"`
	Title             string         `json:"title"`
	SenderEchoID      string         `json:"sender_echo_id"`
	SenderFingerprint string         `json:"sender_fingerprint"`
	CreatedAt         string         `json:"created_at"`
	Files             []ManifestFile `json:"files"`
}
