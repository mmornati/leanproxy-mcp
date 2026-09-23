package responsecache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Key derives the cache key for a tool call, per issue #299:
//
//	SHA-256(server + "\x00" + tool + "\x00" + canonical JSON of args)
//
// Callers must pass the ORIGINAL, unredacted server/tool/args — that is the
// whole point of keying before redaction (two calls that differ only in a
// secret argument value must never collide, which a post-redaction key would
// allow since redaction collapses distinct secrets to the same placeholder).
// Only the resulting hash is kept; the raw arguments are never stored.
func Key(server, tool string, args []byte) string {
	canon, err := canonicalizeJSON(args)
	if err != nil {
		// args is not valid JSON (unusual for tool call arguments); fall
		// back to hashing the raw bytes. Still pre-redaction, still never
		// stored — only the hash below leaves this function.
		canon = args
	}
	h := sha256.New()
	h.Write([]byte(server))
	h.Write([]byte{0})
	h.Write([]byte(tool))
	h.Write([]byte{0})
	h.Write(canon)
	return hex.EncodeToString(h.Sum(nil))
}

// canonicalizeJSON re-encodes args with map keys in a deterministic order so
// that two byte-different-but-semantically-equal argument payloads (e.g.
// differing only in key order or whitespace) hash to the same key.
// encoding/json sorts map[string]interface{} keys when marshaling, which
// gives us canonical form "for free" on the re-encode.
func canonicalizeJSON(args []byte) ([]byte, error) {
	if len(args) == 0 {
		return []byte("null"), nil
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
