package responsecache

import "testing"

func TestKey_Deterministic(t *testing.T) {
	k1 := Key("github", "get_file_contents", []byte(`{"path":"a","owner":"b"}`))
	k2 := Key("github", "get_file_contents", []byte(`{"owner":"b","path":"a"}`))
	if k1 != k2 {
		t.Errorf("Key() not order-independent: %q != %q", k1, k2)
	}
}

func TestKey_DifferentArgsDifferentKey(t *testing.T) {
	k1 := Key("github", "get_file_contents", []byte(`{"path":"a"}`))
	k2 := Key("github", "get_file_contents", []byte(`{"path":"b"}`))
	if k1 == k2 {
		t.Error("Key() collided for different arguments")
	}
}

// The whole point of pre-redaction keying: two calls whose only difference
// is a secret value (which redaction would otherwise collapse to the same
// placeholder) must never collide.
func TestKey_DifferentSecretsNeverCollide(t *testing.T) {
	k1 := Key("aws", "put_object", []byte(`{"key":"AKIAIOSFODNN7EXAMPLE"}`))
	k2 := Key("aws", "put_object", []byte(`{"key":"AKIABBBBBBBBBBBBBBBB"}`))
	if k1 == k2 {
		t.Error("Key() collided for two different secret argument values")
	}
}

func TestKey_DifferentServerOrToolDifferentKey(t *testing.T) {
	base := Key("github", "get_file_contents", []byte(`{}`))
	if Key("gitlab", "get_file_contents", []byte(`{}`)) == base {
		t.Error("Key() ignored server")
	}
	if Key("github", "create_issue", []byte(`{}`)) == base {
		t.Error("Key() ignored tool")
	}
}

func TestKey_EmptyArgs(t *testing.T) {
	k1 := Key("github", "list_servers", nil)
	k2 := Key("github", "list_servers", []byte{})
	if k1 != k2 {
		t.Error("Key() should treat nil and empty args the same")
	}
}

func TestKey_InvalidJSONFallsBackToRawBytes(t *testing.T) {
	// Not valid JSON; Key must not panic and must still be deterministic.
	k1 := Key("s", "t", []byte(`not json`))
	k2 := Key("s", "t", []byte(`not json`))
	if k1 != k2 {
		t.Error("Key() not deterministic for non-JSON args")
	}
	if Key("s", "t", []byte(`also not json`)) == k1 {
		t.Error("Key() collided for different non-JSON args")
	}
}
