package files

import (
	"errors"
	"fmt"
	"strings"
)

// GitShowCommit returns `git show --stat --patch --no-color <hash>`: the
// commit's message, its stat summary and its unified patch, which the review
// tab shows below the history.
func GitShowCommit(root, hash string) (string, error) {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return "", errors.New("files: empty commit hash")
	}
	// A hash comes from git log, but it still reaches argv: refuse anything
	// that could read as another option or as several arguments.
	if strings.HasPrefix(hash, "-") || strings.ContainsAny(hash, " \t\r\n\x00") {
		return "", fmt.Errorf("files: invalid commit hash %q", hash)
	}
	out, err := git(root, "show", "--stat", "--patch", "--no-color", hash)
	if err != nil {
		return string(out), fmt.Errorf("git show: %w", err)
	}
	return string(out), nil
}
