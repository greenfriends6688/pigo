package main

import "testing"

// TestCountPatchStats pins the +N −M counting of the review list: only hunk
// bodies count, so file headers, index lines and the "no newline" marker
// never leak into the numbers.
func TestCountPatchStats(t *testing.T) {
	tests := []struct {
		name     string
		patch    string
		add, del int
	}{
		{
			name: "ordinary hunk",
			patch: "diff --git a/a.go b/a.go\n" +
				"index 111..222 100644\n" +
				"--- a/a.go\n" +
				"+++ b/a.go\n" +
				"@@ -1,3 +1,4 @@\n" +
				" package main\n" +
				"-func old() {}\n" +
				"+func new() {}\n" +
				"+func extra() {}\n" +
				" \n" +
				"\\ No newline at end of file\n",
			add: 2, del: 1,
		},
		{
			name:  "empty patch",
			patch: "",
			add:   0, del: 0,
		},
		{
			name:  "binary patch",
			patch: "diff --git a/img.png b/img.png\nBinary files a/img.png and b/img.png differ\n",
			add:   0, del: 0,
		},
		{
			name: "headers only",
			patch: "diff --git a/a.go b/a.go\n" +
				"--- a/a.go\n" +
				"+++ b/a.go\n",
			add: 0, del: 0,
		},
		{
			name: "content that looks like a header",
			patch: "diff --git a/a.md b/a.md\n" +
				"--- a/a.md\n" +
				"+++ b/a.md\n" +
				"@@ -1,2 +1,2 @@\n" +
				"-+++ not a header\n" +
				"+--- not a header\n",
			add: 1, del: 1,
		},
		{
			name: "two files in one patch",
			patch: "diff --git a/a.go b/a.go\n" +
				"--- a/a.go\n" +
				"+++ b/a.go\n" +
				"@@ -1 +1 @@\n" +
				"-a\n" +
				"+b\n" +
				"diff --git a/b.go b/b.go\n" +
				"--- a/b.go\n" +
				"+++ b/b.go\n" +
				"@@ -1 +1,2 @@\n" +
				"-c\n" +
				"+d\n" +
				"+e\n",
			add: 3, del: 2,
		},
		{
			// An untracked file has no patch; reviewFileStats counts its body
			// with countLines instead, so the patch counter must stay silent.
			name:  "plain body has no hunk",
			patch: "line one\nline two\nline three\n",
			add:   0, del: 0,
		},
		{
			// The new-file patch selectReviewFile builds: one hunk, all added.
			name: "new file patch is all additions",
			patch: "--- a/new.go\n+++ b/new.go\n" +
				"@@ -0,0 +1,3 @@\n" +
				"+package main\n" +
				"+\n" +
				"+func main() {}\n",
			add: 3, del: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			add, del := countPatchStats(tc.patch)
			if add != tc.add || del != tc.del {
				t.Fatalf("countPatchStats = +%d −%d, want +%d −%d", add, del, tc.add, tc.del)
			}
		})
	}
}

// TestCountLines covers the untracked path: a whole body is additions, so its
// line count is the +N the row shows.
func TestCountLines(t *testing.T) {
	tests := []struct {
		body string
		want int
	}{
		{"", 0},
		{"one\n", 1},
		{"one", 1},
		{"one\ntwo\n", 2},
		{"one\ntwo", 2},
		{"one\ntwo\n\n", 3},
	}
	for _, tc := range tests {
		if got := countLines(tc.body); got != tc.want {
			t.Fatalf("countLines(%q) = %d, want %d", tc.body, got, tc.want)
		}
	}
}

// TestReviewStatKey covers the keys the list rows and the stats map share:
// a rename resolves to its destination and a directory loses its slash.
func TestReviewStatKey(t *testing.T) {
	tests := []struct{ path, want string }{
		{"a.go", "a.go"},
		{"pkg/a.go", "pkg/a.go"},
		{"pkg/", "pkg"},
		{"old.go -> new.go", "new.go"},
		{"pkg/old.go -> pkg/new.go", "pkg/new.go"},
	}
	for _, tc := range tests {
		if got := reviewStatKey(tc.path); got != tc.want {
			t.Fatalf("reviewStatKey(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
