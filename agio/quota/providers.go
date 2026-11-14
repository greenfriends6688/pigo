package quota

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"pigo/agio/agent"
)

const (
	anthropicOrigin = "https://api.anthropic.com"
	chatGPTOrigin   = "https://chatgpt.com"
	grokOrigin      = "https://cli-chat-proxy.grok.com"
	deepSeekOrigin  = "https://api.deepseek.com"
	// The usage endpoint rejects requests without this beta header.
	oauthBeta = "oauth-2025-04-20"
	// Claude Code stores its OAuth credential under this keychain service.
	claudeKeychainService = "Claude Code-credentials"
)

// claude reads the subscription's session and weekly windows.
func (s Source) claude(ctx context.Context) (Usage, error) {
	t, err := s.pick(s.claudeTokens())
	if err != nil {
		return Usage{}, err
	}
	headers := map[string]string{
		"Authorization":  "Bearer " + t.access,
		"anthropic-beta": oauthBeta,
		"User-Agent":     "claude-code/2.1.0",
	}
	var body claudeUsage
	if err := s.get(ctx, s.url(Claude, anthropicOrigin, "/api/oauth/usage"), headers, &body); err != nil {
		return Usage{}, err
	}
	u := body.usage()
	u.Plan = t.plan
	// The credential's tier is login-time metadata that survives plan
	// changes; the profile's organization names the current plan.
	var profile struct {
		Organization struct {
			Type string `json:"organization_type"`
			Tier string `json:"rate_limit_tier"`
		} `json:"organization"`
	}
	if s.get(ctx, s.url(Claude, anthropicOrigin, "/api/oauth/profile"), headers, &profile) == nil {
		if plan := claudePlan(strings.TrimPrefix(profile.Organization.Type, "claude_"), profile.Organization.Tier); plan != "" {
			u.Plan = plan
		}
	}
	return u, nil
}

// claudeTokens lists Claude Code's credential (keychain first on macOS,
// then the credentials file) and then pi's.
func (s Source) claudeTokens() []token {
	var payloads []string
	if s.Keychain != nil {
		if p, err := s.Keychain(claudeKeychainService); err == nil {
			payloads = append(payloads, p)
		}
	}
	if data, err := os.ReadFile(filepath.Join(s.dir("CLAUDE_CONFIG_DIR", ".claude"), ".credentials.json")); err == nil {
		payloads = append(payloads, string(data))
	}
	var tokens []token
	for _, p := range payloads {
		var v struct {
			OAuth map[string]any `json:"claudeAiOauth"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(p)), &v) == nil && v.OAuth != nil {
			tokens = append(tokens, token{access: str(v.OAuth, "accessToken"), expires: millis(v.OAuth, "expiresAt"),
				plan: claudePlan(str(v.OAuth, "subscriptionType"), str(v.OAuth, "rateLimitTier"))})
		}
	}
	if auth := s.piAuth("anthropic"); str(auth, "type") == "oauth" {
		tokens = append(tokens, token{access: str(auth, "access"), expires: millis(auth, "expires")})
	}
	return tokens
}

type claudeUsage struct {
	Limits []struct {
		Kind     string   `json:"kind"`
		Percent  *float64 `json:"percent"`
		ResetsAt string   `json:"resets_at"`
		Scope    *struct {
			Model *struct {
				DisplayName string `json:"display_name"`
			} `json:"model"`
		} `json:"scope"`
	} `json:"limits"`
	FiveHour       *claudeFlat `json:"five_hour"`
	SevenDay       *claudeFlat `json:"seven_day"`
	SevenDayOpus   *claudeFlat `json:"seven_day_opus"`
	SevenDaySonnet *claudeFlat `json:"seven_day_sonnet"`
}

type claudeFlat struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

const (
	fiveHours = 5 * 60
	week      = 7 * 24 * 60
)

// usage prefers the limits array and falls back to the older flat fields.
func (b claudeUsage) usage() Usage {
	var u Usage
	for _, l := range b.Limits {
		if l.Percent == nil {
			continue
		}
		w := Window{Percent: clamp(*l.Percent), ResetsAt: parseTime(l.ResetsAt)}
		switch l.Kind {
		case "session":
			w.Minutes = fiveHours
		case "weekly_all":
			w.Minutes = week
		case "weekly_scoped":
			w.Minutes, w.Scope = week, "model"
			if l.Scope != nil && l.Scope.Model != nil && l.Scope.Model.DisplayName != "" {
				w.Scope = l.Scope.Model.DisplayName
			}
		default:
			continue // overage and credit lanes
		}
		u.Windows = append(u.Windows, w)
	}
	if len(u.Windows) > 0 {
		return u
	}
	for _, f := range []struct {
		window  *claudeFlat
		minutes int
		scope   string
	}{{b.FiveHour, fiveHours, ""}, {b.SevenDay, week, ""}, {b.SevenDayOpus, week, "Opus"}, {b.SevenDaySonnet, week, "Sonnet"}} {
		if f.window != nil && f.window.Utilization != nil {
			u.Windows = append(u.Windows, Window{Minutes: f.minutes, Scope: f.scope, Percent: clamp(*f.window.Utilization),
				ResetsAt: parseTime(f.window.ResetsAt)})
		}
	}
	return u
}

// claudePlan names a plan such as "Max (5x)" from a subscription type
// ("max") and rate-limit tier ("default_claude_max_5x").
func claudePlan(subscription, tier string) string {
	words := strings.FieldsFunc(strings.ToLower(tier), func(r rune) bool { return r == '_' || r == '-' || r == ' ' })
	base := strings.ToLower(subscription)
	if base == "" {
		for _, plan := range []string{"max", "pro", "team", "enterprise"} {
			for _, w := range words {
				if w == plan && base == "" {
					base = plan
				}
			}
		}
	}
	if base == "" {
		return ""
	}
	label := title(base)
	if base == "max" {
		for i, w := range words {
			if w == "max" && i+1 < len(words) && strings.HasSuffix(words[i+1], "x") && len(words[i+1]) > 1 &&
				strings.Trim(words[i+1][:len(words[i+1])-1], "0123456789") == "" {
				label += " (" + words[i+1] + ")"
				break
			}
		}
	}
	return label
}

// codex reads the Codex windows of the ChatGPT account the first usable
// token signs in to.
func (s Source) codex(ctx context.Context, p Provider, tokens []token) (Usage, error) {
	t, err := s.pick(tokens)
	if err != nil {
		return Usage{}, err
	}
	headers := map[string]string{"Authorization": "Bearer " + t.access, "User-Agent": agent.Client.Name}
	if t.account != "" {
		headers["ChatGPT-Account-Id"] = t.account
	}
	var body codexUsage
	if err := s.get(ctx, s.url(p, chatGPTOrigin, "/backend-api/wham/usage"), headers, &body); err != nil {
		return Usage{}, err
	}
	u := body.usage()
	if len(u.Windows) == 0 {
		return Usage{}, errors.New("no rate limits reported")
	}
	return u, nil
}

// codexTokens lists Codex's ChatGPT login.
func (s Source) codexTokens() []token {
	var tokens []token
	if data, err := os.ReadFile(filepath.Join(s.dir("CODEX_HOME", ".codex"), "auth.json")); err == nil {
		var auth struct {
			Tokens *struct {
				Access  string `json:"access_token"`
				Account string `json:"account_id"`
			} `json:"tokens"`
		}
		if json.Unmarshal(data, &auth) == nil && auth.Tokens != nil {
			tokens = append(tokens, token{access: auth.Tokens.Access, account: auth.Tokens.Account, expires: jwtExpiry(auth.Tokens.Access)})
		}
	}
	return tokens
}

// piCodexTokens lists pi's ChatGPT login, whose account comes from its JWT
// claim when pi did not store it.
func (s Source) piCodexTokens() []token {
	auth := s.piAuth("openai-codex")
	if str(auth, "type") != "oauth" {
		return nil
	}
	t := token{access: str(auth, "access"), expires: millis(auth, "expires"), account: str(auth, "accountId")}
	if t.account == "" {
		if claims, ok := jwtClaims(t.access)["https://api.openai.com/auth"].(map[string]any); ok {
			t.account = str(claims, "chatgpt_account_id")
		}
	}
	return []token{t}
}

func jwtExpiry(access string) time.Time {
	if exp, ok := jwtClaims(access)["exp"].(float64); ok {
		return time.Unix(int64(exp), 0)
	}
	return time.Time{}
}

type codexUsage struct {
	Plan       string          `json:"plan_type"`
	RateLimit  *codexRateLimit `json:"rate_limit"`
	Additional []struct {
		Name      string          `json:"limit_name"`
		RateLimit *codexRateLimit `json:"rate_limit"`
	} `json:"additional_rate_limits"`
}

type codexRateLimit struct {
	Primary   *codexWindow `json:"primary_window"`
	Secondary *codexWindow `json:"secondary_window"`
}

type codexWindow struct {
	Used    *float64 `json:"used_percent"`
	Seconds int      `json:"limit_window_seconds"`
	ResetAt int64    `json:"reset_at"`
}

func (b codexUsage) usage() Usage {
	u := Usage{Plan: chatGPTPlan(b.Plan)}
	add := func(r *codexRateLimit, scope string) {
		if r == nil {
			return
		}
		for _, w := range []*codexWindow{r.Primary, r.Secondary} {
			if w == nil || w.Used == nil {
				continue
			}
			window := Window{Minutes: w.Seconds / 60, Scope: scope, Percent: clamp(*w.Used)}
			if w.ResetAt > 0 {
				window.ResetsAt = time.Unix(w.ResetAt, 0)
			}
			u.Windows = append(u.Windows, window)
		}
	}
	add(b.RateLimit, "")
	for _, a := range b.Additional {
		// The Spark lane is promotional quota, not a limit people plan for.
		if !strings.Contains(strings.ToLower(a.Name), "spark") {
			add(a.RateLimit, a.Name)
		}
	}
	return u
}

// chatGPTPlan names ChatGPT plans; plain pro is the 20x plan and prolite
// the 5x one. Unknown tiers stay unnamed.
func chatGPTPlan(plan string) string {
	switch plan {
	case "free", "free_workspace", "guest":
		return "Free"
	case "go":
		return "Go"
	case "plus":
		return "Plus"
	case "pro":
		return "Pro (20x)"
	case "prolite", "pro_lite":
		return "Pro (5x)"
	case "team":
		return "Team"
	case "business", "self_serve_business_usage_based":
		return "Business"
	case "enterprise", "enterprise_cbp_usage_based":
		return "Enterprise"
	case "edu", "education", "k12":
		return "Edu"
	}
	return ""
}

// xai reads the included usage of the xAI subscription the first usable
// token signs in to for the current period.
func (s Source) xai(ctx context.Context, p Provider, tokens []token) (Usage, error) {
	t, err := s.pick(tokens)
	if err != nil {
		return Usage{}, err
	}
	headers := map[string]string{"Authorization": "Bearer " + t.access}
	var body struct {
		Config struct {
			Percent *float64  `json:"creditUsagePercent"`
			Period  xaiPeriod `json:"currentPeriod"`
		} `json:"config"`
	}
	if err := s.get(ctx, s.url(p, grokOrigin, "/v1/billing?format=credits"), headers, &body); err != nil {
		return Usage{}, err
	}
	if body.Config.Percent == nil {
		return Usage{}, errors.New("no usage reported")
	}
	start, end := parseTime(body.Config.Period.Start), parseTime(body.Config.Period.End)
	u := Usage{Windows: []Window{{Minutes: periodMinutes(start, end), Percent: clamp(*body.Config.Percent), ResetsAt: end}}}
	var settings struct {
		Tier string `json:"subscription_tier_display"`
	}
	if s.get(ctx, s.url(p, grokOrigin, "/v1/settings"), headers, &settings) == nil {
		u.Plan = strings.TrimSpace(settings.Tier)
	}
	return u, nil
}

type xaiPeriod struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// piXAITokens lists pi's xAI login.
func (s Source) piXAITokens() []token {
	if auth := s.piAuth("xai"); str(auth, "type") == "oauth" {
		return []token{{access: str(auth, "access"), expires: millis(auth, "expires")}}
	}
	return nil
}

// periodMinutes is a usage period's length, 0 when unknown.
func periodMinutes(start, end time.Time) int {
	if start.IsZero() || !end.After(start) {
		return 0
	}
	return int(end.Sub(start).Round(time.Minute) / time.Minute)
}

// deepSeek reads the account balance.
func (s Source) deepSeek(ctx context.Context) (Usage, error) {
	key := s.deepSeekKey()
	if key == "" {
		return Usage{}, ErrNoCredentials
	}
	var body struct {
		Balances []struct {
			Currency string `json:"currency"`
			Total    string `json:"total_balance"`
		} `json:"balance_infos"`
	}
	if err := s.get(ctx, s.url(DeepSeek, deepSeekOrigin, "/user/balance"), map[string]string{"Authorization": "Bearer " + key}, &body); err != nil {
		return Usage{}, err
	}
	var u Usage
	for _, b := range body.Balances {
		u.Balances = append(u.Balances, Balance{Currency: b.Currency, Amount: formatAmount(b.Total)})
	}
	if len(u.Balances) == 0 {
		return Usage{}, errors.New("no balance reported")
	}
	return u, nil
}

// deepSeekKey follows pi: a stored key wins over DEEPSEEK_API_KEY, and a
// stored key naming an environment variable reads it. Command keys ("!…")
// are not run.
func (s Source) deepSeekKey() string {
	if auth := s.piAuth("deepseek"); str(auth, "type") == "api_key" {
		key := str(auth, "key")
		if strings.HasPrefix(key, "!") {
			return ""
		}
		if v := s.env(key); v != "" {
			return strings.TrimSpace(v)
		}
		if key != "" {
			return key
		}
	}
	return strings.TrimSpace(s.env("DEEPSEEK_API_KEY"))
}
