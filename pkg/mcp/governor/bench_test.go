package governor

import (
	"strings"
	"testing"
)

func BenchmarkTruncateText200KB(b *testing.B) {
	s := bigText(3500)
	b.SetBytes(int64(len(s)))
	for i := 0; i < b.N; i++ {
		TruncateText(s, 16000, testID)
	}
}

func BenchmarkTruncateJSON1000(b *testing.B) {
	doc := jsonArray(1000)
	b.SetBytes(int64(len(doc)))
	for i := 0; i < b.N; i++ {
		TruncateJSON(doc, 16000, testID)
	}
}

func BenchmarkTruncateJSONNested1MB(b *testing.B) {
	doc := []byte(`{"total":20000,"items":` + string(jsonArray(15000)) + `,"note":"` + strings.Repeat("n", 1000) + `"}`)
	b.SetBytes(int64(len(doc)))
	for i := 0; i < b.N; i++ {
		TruncateJSON(doc, 16000, testID)
	}
}
