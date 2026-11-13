package sessions

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// makeScaleTree creates count session files, one per project directory, with
// a header and a first user message. Used by the PLAN §8 P3 acceptance
// benchmarks (3000 sessions, cold < 2 s, cached < 300 ms).
func makeScaleTree(b *testing.B, count int) string {
	b.Helper()
	root := b.TempDir()
	sessions := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		b.Fatal(err)
	}
	dir := filepath.Join(sessions, DirName("/bench/project"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < count; i++ {
		p := filepath.Join(dir, fmt.Sprintf("2026-01-01T00-00-00-000Z_%08x.jsonl", i))
		body := fmt.Sprintf(
			"{\"type\":\"session\",\"version\":3,\"id\":\"%08x\",\"timestamp\":\"2026-01-01T00:00:00.000Z\",\"cwd\":\"/bench/project\"}\n{\"type\":\"message\",\"id\":\"m%d\",\"parentId\":null,\"timestamp\":\"2026-01-01T00:00:01.000Z\",\"message\":{\"role\":\"user\",\"content\":\"session number %d\"}}\n", i, i, i)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	return root
}

func BenchmarkScan3000Cold(b *testing.B) {
	root := makeScaleTree(b, 3000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		infos, err := Scan(root)
		if err != nil || len(infos) != 3000 {
			b.Fatalf("scan = %d, %v", len(infos), err)
		}
	}
}

func BenchmarkStoreCachedRefresh(b *testing.B) {
	root := makeScaleTree(b, 3000)
	store := NewStore(root)
	if err := store.Refresh(); err != nil {
		b.Fatal(err)
	}
	if err := store.SaveCache(); err != nil {
		b.Fatal(err)
	}
	loaded := NewStore(root)
	if err := loaded.LoadCache(); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		changed, err := loaded.RefreshIfChanged()
		if err != nil {
			b.Fatal(err)
		}
		if changed {
			b.Fatal("unchanged tree must not trigger a rescan")
		}
	}
}
