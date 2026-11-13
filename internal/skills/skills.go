// Package skills talks to skills.sh and the skills CLI: search the public
// registry, and install a package into pi's agent directory.
package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// Hit is one search result.
type Hit struct {
	Package  string
	Installs string
	URL      string
}

// Search queries skills.sh's registry, the same endpoint pi-web's Settings ›
// Skills uses. It returns at most limit results, most installed first.
func Search(ctx context.Context, query string, limit int) ([]Hit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("skills: empty query")
	}
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	url := fmt.Sprintf("https://skills.sh/api/search?q=%s&limit=%d", urlQueryEscape(query), limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("skills: search HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Skills []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Source   string `json:"source"`
			Installs int    `json:"installs"`
		} `json:"skills"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	out := make([]Hit, 0, len(payload.Skills))
	for _, s := range payload.Skills {
		name := strings.TrimSpace(s.Name)
		source := strings.TrimSpace(s.Source)
		id := strings.TrimSpace(s.ID)
		if name == "" || (source == "" && id == "") {
			continue
		}
		pkg := source
		if pkg == "" {
			pkg = id
		}
		pkg += "@" + name
		installs := ""
		if s.Installs > 0 {
			installs = formatCount(s.Installs) + " installs"
		}
		link := ""
		if id != "" {
			link = "https://skills.sh/" + id
		}
		out = append(out, Hit{Package: pkg, Installs: installs, URL: link})
	}
	return out, nil
}

func urlQueryEscape(s string) string {
	r := strings.NewReplacer(" ", "%20", "+", "%2B", "&", "%26", "=", "%3D", "#", "%23", "?", "%3F", "/", "%2F")
	return r.Replace(s)
}

func formatCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

// Install adds pkg globally for pi with `npx skills add … -y --agent pi -g`,
// as pi-web's route does. It returns the CLI's combined output.
func Install(ctx context.Context, pkg string) (string, error) {
	pkg = strings.TrimSpace(pkg)
	if pkg == "" {
		return "", fmt.Errorf("skills: empty package")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "npx", "skills", "add", pkg, "-y", "--agent", "pi", "-g")
	cmd.Env = append(cmd.Environ(), "FORCE_COLOR=0")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	text := out.String()
	if err != nil {
		return text, fmt.Errorf("npx skills add: %w", err)
	}
	return text, nil
}
