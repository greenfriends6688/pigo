// Package quota reads the account usage limits of model providers the way
// their own CLIs read them: Claude's subscription windows from
// api.anthropic.com with Claude Code's or pi's OAuth token, ChatGPT's Codex
// windows from the ChatGPT backend and xAI's included usage from its billing
// endpoint, each for a CLI's and pi's logins as separate accounts, and
// DeepSeek's balance with pi's or the environment's API key. Credentials are
// only read, never refreshed or written. Everything blocks on files and the
// network and must run off the UI thread.
package quota

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"pigo/agio/agent"
)

// Provider identifies an account whose limits can be read.
type Provider string

const (
	Claude   Provider = "claude"
	Codex    Provider = "codex"
	DeepSeek Provider = "deepseek"
	// PiCodex and PiXAI are pi's own ChatGPT and xAI logins, read apart
	// from Codex's, which may sign in to another account.
	PiCodex Provider = "pi-codex"
	PiXAI   Provider = "pi-xai"
)

// Providers lists the readable accounts in presentation order.
var Providers = []Provider{Claude, Codex, PiCodex, PiXAI, DeepSeek}

// Name is the account's product name.
func (p Provider) Name() string {
	switch p {
	case Claude:
		return "Claude"
	case Codex:
		return "Codex"
	case PiCodex:
		return "Codex (pi)"
	case PiXAI:
		return "xAI (pi)"
	case DeepSeek:
		return "DeepSeek"
	}
	return string(p)
}

// UsagePage is the provider's web page showing the account's usage.
func (p Provider) UsagePage() string {
	switch p {
	case Claude:
		return "https://claude.ai/new#settings/usage"
	case Codex, PiCodex:
		return "https://chatgpt.com/settings/usage?tab=overview"
	case PiXAI:
		return "https://grok.com/?_s=usage"
	case DeepSeek:
		return "https://platform.deepseek.com/usage"
	}
	return ""
}

// Usage is one account's limits.
type Usage struct {
	// Plan names the subscription, such as "Max (20x)"; empty when unknown.
	Plan     string
	Windows  []Window
	Balances []Balance
}

// Window is a rate-limit lane. Minutes is its length, 0 when unknown; Scope
// names the model of a model-scoped lane. Percent is the used share, 0..100.
type Window struct {
	Minutes  int
	Scope    string
	Percent  float64
	ResetsAt time.Time
}

// ResetDelay is how long after a window resets its account is read again,
// giving the provider time to report the new limits.
const ResetDelay = 5 * time.Second

// NextReset is the earliest window reset after the given time, or zero.
func (u *Usage) NextReset(after time.Time) time.Time {
	var next time.Time
	if u == nil {
		return next
	}
	for _, w := range u.Windows {
		if w.ResetsAt.After(after) && (next.IsZero() || w.ResetsAt.Before(next)) {
			next = w.ResetsAt
		}
	}
	return next
}

// Balance is prepaid credit in a currency.
type Balance struct {
	Currency, Amount string
}

var (
	// ErrNoCredentials means the account is not signed in on this machine.
	ErrNoCredentials = errors.New("no credentials")
	// ErrExpired means the stored token expired; its CLI refreshes it on use.
	ErrExpired = errors.New("credentials expired")
)

// StatusError is an unexpected HTTP response; Message is the provider's own
// explanation when it sent one and RetryAfter how long it asked to wait.
type StatusError struct {
	Status     int
	Message    string
	RetryAfter time.Duration
}

func (e *StatusError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

// Source locates credentials and reaches the providers.
type Source struct {
	// Home is the user's home directory; Env the login environment, which
	// may relocate the CLIs' configuration and supply API keys.
	Home string
	Env  []string
	// Keychain reads a macOS generic password by service; nil skips it.
	Keychain func(service string) (string, error)
	// Client defaults to http.DefaultClient; Now to time.Now.
	Client *http.Client
	Now    func() time.Time
	// BaseURLs replace provider origins in tests.
	BaseURLs map[Provider]string
}

// SystemKeychain reads the login keychain with /usr/bin/security on macOS;
// it is nil elsewhere.
var SystemKeychain = systemKeychain()

func systemKeychain() func(string) (string, error) {
	if runtime.GOOS != "darwin" {
		return nil
	}
	return func(service string) (string, error) {
		out, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", service, "-w").Output()
		return string(out), err
	}
}

// Fetch reads one account's limits.
func (s Source) Fetch(ctx context.Context, p Provider) (Usage, error) {
	switch p {
	case Claude:
		return s.claude(ctx)
	case Codex:
		return s.codex(ctx, p, s.codexTokens())
	case PiCodex:
		return s.codex(ctx, p, s.piCodexTokens())
	case PiXAI:
		return s.xai(ctx, p, s.piXAITokens())
	case DeepSeek:
		return s.deepSeek(ctx)
	}
	return Usage{}, ErrNoCredentials
}

func (s Source) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s Source) env(name string) string {
	for i := len(s.Env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(s.Env[i], "="); ok && k == name {
			return v
		}
	}
	return ""
}

func (s Source) url(p Provider, origin, path string) string {
	if base := s.BaseURLs[p]; base != "" {
		origin = base
	}
	return origin + path
}

// dir returns the environment's directory override, with ~ expanded, or
// the home-relative fallback.
func (s Source) dir(name string, fallback ...string) string {
	if v := s.env(name); v != "" {
		if rest, ok := strings.CutPrefix(v, "~"); ok {
			return filepath.Join(s.Home, rest)
		}
		return v
	}
	return filepath.Join(append([]string{s.Home}, fallback...)...)
}

// piAuth reads pi's stored credential for a provider; nil when absent.
func (s Source) piAuth(provider string) map[string]any {
	data, err := os.ReadFile(filepath.Join(s.dir("PI_CODING_AGENT_DIR", ".pi", "agent"), "auth.json"))
	if err != nil {
		return nil
	}
	var auth map[string]map[string]any
	if json.Unmarshal(data, &auth) != nil {
		return nil
	}
	return auth[provider]
}

// get requests url and decodes a 200 JSON body into out.
func (s Source) get(ctx context.Context, url string, headers map[string]string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		e := &StatusError{Status: resp.StatusCode, Message: errorMessage(data)}
		if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 {
			e.RetryAfter = time.Duration(seconds) * time.Second
		}
		return e
	}
	return json.Unmarshal(data, out)
}

// errorMessage extracts {"error":{"message":…}}, {"error":"…"} or a
// top-level {"message":…}.
func errorMessage(body []byte) string {
	var v struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(body, &v) != nil {
		return ""
	}
	if len(v.Error) == 0 {
		return strings.TrimSpace(v.Message)
	}
	var nested struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(v.Error, &nested) == nil {
		return strings.TrimSpace(nested.Message)
	}
	var text string
	if json.Unmarshal(v.Error, &text) == nil {
		return strings.TrimSpace(text)
	}
	return ""
}

func str(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return strings.TrimSpace(v)
}

// millis converts an epoch-milliseconds value; zero when absent.
func millis(m map[string]any, key string) time.Time {
	if v, ok := m[key].(float64); ok && v > 0 {
		return time.UnixMilli(int64(v))
	}
	return time.Time{}
}

// jwtClaims decodes a JWT payload without verifying it.
func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(data, &claims) != nil {
		return nil
	}
	return claims
}

func clamp(percent float64) float64 { return min(100, max(0, percent)) }

func parseTime(v string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, v)
	return t
}

// token is a bearer credential and when it expires, zero when unknown.
type token struct {
	access  string
	expires time.Time
	plan    string
	account string
}

// pick returns the first unexpired token, ErrExpired when all found have
// expired and ErrNoCredentials when none was found.
func (s Source) pick(tokens []token) (token, error) {
	err := ErrNoCredentials
	for _, t := range tokens {
		if t.access == "" {
			continue
		}
		if !t.expires.IsZero() && !t.expires.After(s.now()) {
			err = ErrExpired
			continue
		}
		return t, nil
	}
	return token{}, err
}

func title(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func formatAmount(v string) string {
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return strconv.FormatFloat(f, 'f', 2, 64)
	}
	return v
}

// For maps a model to the account whose limits it uses.
func For(m agent.Model) (Provider, bool) {
	switch m.Tool {
	case agent.Claude:
		return Claude, true
	case agent.Codex:
		return Codex, true
	case agent.Pi:
		switch m.Provider {
		case "anthropic":
			return Claude, true
		case "openai", "openai-codex":
			return PiCodex, true
		case "deepseek":
			return DeepSeek, true
		case "xai":
			return PiXAI, true
		}
	}
	return "", false
}

// Reader reads accounts with the credentials under home, with the system
// keychain where there is one.
func Reader(home string) func(ctx context.Context, env []string, p Provider) (Usage, error) {
	return func(ctx context.Context, env []string, p Provider) (Usage, error) {
		src := Source{Home: home, Env: env, Keychain: SystemKeychain, Client: &http.Client{Timeout: 20 * time.Second}}
		return src.Fetch(ctx, p)
	}
}
