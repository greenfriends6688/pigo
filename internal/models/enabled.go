// Editing of pi's `enabledModels` setting, mirroring pi-web's
// lib/enabled-models.ts. `enabledModels` is a whitelist of pi `--models`
// patterns — "*", "provider/*", "provider/modelId", each optionally followed by
// a ":level" thinking pin — and an absent or empty list means "no scope", so pi
// enables every model.
//
// Every toggle is the smallest edit of the existing list: a pattern that covers
// nothing the caller can see is never rewritten (its provider may just be
// signed out, its model renamed, or it may have been written on another
// machine), entry order is kept because it is pi's model cycle order, and
// thinking pins survive. Narrowing a glob means spelling out the models that
// stay, which is why the setters accept the available model references.

package models

import (
	"errors"
	"fmt"
	"strings"
)

// thinkingLevels are the suffixes pi accepts as a thinking pin. Model ids may
// contain colons, so only a known level after the last colon is a pin.
var thinkingLevels = map[string]bool{
	"off": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true,
}

var (
	// ErrLastModel is returned when an edit would leave no enabled model. pi
	// reads an empty enabledModels as "no scope" and enables everything, so
	// storing it would mean the opposite of what was asked.
	ErrLastModel = errors.New("models: cannot disable the last enabled model")
	// ErrNeedModels is returned when a glob has to be narrowed but no available
	// model references were passed, so the models that should stay enabled are
	// unknown. Rewriting the glob then would silently drop them.
	ErrNeedModels = errors.New("models: cannot narrow a glob without the available model ids")
)

const enabledKey = "enabledModels"

// ReadEnabled returns the enabledModels patterns. A missing key, an empty list
// or a list of blanks means every model is enabled and yields nil.
func ReadEnabled(settings map[string]any) []string {
	var list []string
	switch raw := settings[enabledKey].(type) {
	case []string:
		list = raw
	case []any:
		list = make([]string, 0, len(raw))
		for _, v := range raw {
			if s, ok := v.(string); ok {
				list = append(list, s)
			}
		}
	default:
		return nil
	}
	out := make([]string, 0, len(list))
	for _, pattern := range list {
		if strings.TrimSpace(pattern) != "" {
			out = append(out, pattern)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// EnabledFor reports whether patterns leave one model enabled. No patterns
// means everything is enabled. A pattern that pins a thinking level still
// enables the model; the pin only changes the level it runs at. A list whose
// patterns all went stale also behaves as "everything" in pi, but that can only
// be judged against a model list, so it reads as "nothing enabled" here.
func EnabledFor(patterns []string, provider, id string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		if patternCovers(pattern, provider, id) {
			return true
		}
	}
	return false
}

// ProviderCovered counts how many of ids patterns leave enabled, for a panel
// showing "3/5".
func ProviderCovered(patterns []string, provider string, ids []string) int {
	if len(patterns) == 0 {
		return len(ids)
	}
	n := 0
	for _, id := range ids {
		if EnabledFor(patterns, provider, id) {
			n++
		}
	}
	return n
}

// SetEnabled switches one model on or off with the smallest edit of the
// enabledModels list and writes it back into settings.
//
// Turning a model off expands every pattern that really covers it into
// explicit "provider/modelId" entries for the models that stay, keeping each
// pattern's ":level" pin; every other pattern is left verbatim. A glob cannot
// be narrowed
// without knowing what it covers, so available must name the provider's models
// (every available ref when a bare "*" is involved, and the same list is what
// the last-model guard counts). Without it, an edit that has to expand a glob
// returns ErrNeedModels. Disabling the last enabled model returns ErrLastModel
// and leaves settings untouched.
//
// Turning a model on appends "provider/modelId" unless a pattern already covers
// it, and does nothing at all while nothing narrows the list.
func SetEnabled(settings map[string]any, provider, id string, on bool, available ...string) error {
	provider, id = strings.TrimSpace(provider), strings.TrimSpace(id)
	if provider == "" || id == "" {
		return fmt.Errorf("models: provider and model id are required")
	}
	patterns := ReadEnabled(settings)
	if on {
		if EnabledFor(patterns, provider, id) {
			return nil
		}
		return storeEnabled(settings, append(patterns, provider+"/"+id))
	}
	// One model is not the provider's model list, so a provider glob may not be
	// expanded on the strength of the targets alone.
	return setDisabled(settings, patterns, []string{provider + "/" + id}, available, false)
}

// SetProviderEnabled switches several models of one provider on or off, keeping
// every other entry in place and in order. ids are the provider's models as the
// panel shows them, and they are trusted as its complete list, so a "provider/*"
// pattern can be dropped once all of them go. available is the model list the
// single-model setter takes; pass it as well to narrow a bare "*", and to have
// the last-model guard count what is left.
func SetProviderEnabled(settings map[string]any, provider string, ids []string, on bool, available ...string) error {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return fmt.Errorf("models: provider is required")
	}
	refs := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		ref := provider + "/" + id
		if seen[ref] {
			continue
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	if len(refs) == 0 {
		return nil
	}

	patterns := ReadEnabled(settings)
	if on {
		changed := false
		for _, ref := range refs {
			p, id := refParts(ref)
			if EnabledFor(patterns, p, id) {
				continue
			}
			patterns = append(patterns, ref)
			changed = true
		}
		if !changed {
			return nil
		}
		return storeEnabled(settings, patterns)
	}

	// The provider's ids are trusted as its complete model list, so its own
	// glob can be dropped when every model goes; anything wider (a bare "*")
	// still needs the available refs.
	return setDisabled(settings, patterns, refs, available, true)
}

// setDisabled applies an off edit. targets are the refs to switch off, available
// is the model list the caller could see (empty when it passed none), and
// wholeProvider says whether targets enumerate every model of their provider,
// which is what makes a provider glob expendable without that list.
func setDisabled(settings map[string]any, patterns, targets, available []string, wholeProvider bool) error {
	if len(patterns) > 0 && !anyEnabled(patterns, targets) {
		return nil // already off
	}
	next, err := planDisable(patterns, targets, available, wholeProvider)
	if err != nil {
		return err
	}
	if len(next) == 0 {
		return ErrLastModel
	}
	// A list that leaves none of the known models enabled narrows nothing, so pi
	// would enable everything again: the same trap as an empty list.
	if len(available) > 0 && countEnabled(next, dedup(append(append([]string{}, available...), targets...))) == 0 {
		return ErrLastModel
	}
	return storeEnabled(settings, next)
}

// planDisable returns the pattern list with targets switched off.
func planDisable(patterns, targets, available []string, wholeProvider bool) ([]string, error) {
	known := dedup(append(append([]string{}, available...), targets...))
	removed := make(map[string]bool, len(targets))
	for _, target := range targets {
		removed[target] = true
	}

	base := patterns
	if len(base) == 0 {
		// No scope: every available model is enabled. Only the model list can
		// say which ones stay on once a model is dropped from it.
		if len(available) == 0 {
			return nil, ErrNeedModels
		}
		base = []string{"*"}
	}

	out := make([]string, 0, len(base)+len(known))
	for _, pattern := range base {
		body, pin := splitPin(pattern)
		if !coversAny(body, targets) {
			out = append(out, pattern)
			continue
		}
		// Expand only the patterns that really cover a target, in place, and
		// keep the thinking pin on every survivor. A pattern that covered only
		// removed models disappears.
		survivors := 0
		for _, ref := range known {
			if removed[ref] {
				continue
			}
			provider, id := refParts(ref)
			if !matchBody(body, provider, id) {
				continue
			}
			survivors++
			out = append(out, formatRef(ref, pin))
		}
		// A glob with no survivor we know of must not be rewritten blind: that
		// would drop the models of a signed-out provider, a renamed model or
		// another machine's config. A provider-scoped glob is safe when the
		// batch setter vouched for that provider's whole model list.
		if survivors == 0 && isGlob(body) && len(available) == 0 && !scopedToTargets(body, targets, wholeProvider) {
			return nil, ErrNeedModels
		}
	}
	return dedup(out), nil
}

// storeEnabled writes the list back. An empty list is never stored: it would
// enable every model.
func storeEnabled(settings map[string]any, patterns []string) error {
	if len(patterns) == 0 {
		return ErrLastModel
	}
	list := make([]any, len(patterns))
	for i, pattern := range patterns {
		list[i] = pattern
	}
	settings[enabledKey] = list
	return nil
}

// patternCovers reports whether one pattern covers one model.
func patternCovers(pattern, provider, id string) bool {
	body, _ := splitPin(pattern)
	return matchBody(body, provider, id)
}

// coversAny reports whether a pattern body covers one of the given references.
func coversAny(body string, targets []string) bool {
	for _, target := range targets {
		provider, id := refParts(target)
		if target != "" && matchBody(body, provider, id) {
			return true
		}
	}
	return false
}

// scopedToTargets reports whether a pattern is a glob over the targets' own
// provider, so that provider's complete model list covers everything it can
// match.
func scopedToTargets(body string, targets []string, wholeProvider bool) bool {
	if !wholeProvider || len(targets) == 0 {
		return false
	}
	prefix, _, scoped := strings.Cut(body, "/")
	if !scoped {
		return false
	}
	provider, _ := refParts(targets[0])
	return globMatch(prefix, provider)
}

// splitPin separates a trailing thinking level from a pattern body, so
// "anthropic/*:high" is the glob with a pin while "zen/x:exacto" is an id that
// merely contains a colon.
func splitPin(pattern string) (body, pin string) {
	colon := strings.LastIndexByte(pattern, ':')
	if colon <= 0 || !thinkingLevels[pattern[colon+1:]] {
		return pattern, ""
	}
	return pattern[:colon], pattern[colon+1:]
}

// formatRef appends a pin to a model reference.
func formatRef(ref, pin string) string {
	if pin == "" {
		return ref
	}
	return ref + ":" + pin
}

// matchBody reports whether a pattern body covers one model.
//
// The provider prefix is cut at the first slash and the rest is matched against
// the whole model id, so "provider/*" covers ids that contain a slash
// ("commandcode/sakana/fugu-ultra", most OpenRouter ids). minimatch would stop
// "*" at the slash and silently miss them. Matching stays inside the named
// provider, unlike pi's resolver, which also matches a pattern against the bare
// model id and can therefore leak into another provider.
func matchBody(body, provider, id string) bool {
	if body == "" || id == "" {
		return false
	}
	if slash := strings.IndexByte(body, '/'); slash >= 0 {
		rest := body[slash+1:]
		if rest == "" {
			return false
		}
		return globMatch(body[:slash], provider) && globMatch(rest, id)
	}
	// A bare pattern names a model: "gpt-5" covers any provider's gpt-5, and
	// "*" covers everything.
	return globMatch(body, id) || globMatch(body, provider+"/"+id)
}

// isGlob reports whether a pattern body uses wildcards.
func isGlob(body string) bool {
	return strings.ContainsAny(body, "*?")
}

// globMatch is a small minimatch: '*' matches any run of characters, slashes
// included, '?' matches exactly one.
func globMatch(pattern, s string) bool {
	pi, si := 0, 0
	star, mark := -1, 0
	for si < len(s) {
		switch {
		case pi < len(pattern) && (pattern[pi] == '?' || pattern[pi] == s[si]):
			pi++
			si++
		case pi < len(pattern) && pattern[pi] == '*':
			star, mark = pi, si
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			si = mark
		default:
			return false
		}
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}

// refParts splits "provider/modelId" at the first slash, since model ids may
// contain slashes themselves.
func refParts(ref string) (provider, id string) {
	if slash := strings.IndexByte(ref, '/'); slash >= 0 {
		return ref[:slash], ref[slash+1:]
	}
	return ref, ""
}

// countEnabled counts how many references patterns leave enabled.
func countEnabled(patterns, refs []string) int {
	n := 0
	for _, ref := range refs {
		provider, id := refParts(ref)
		if ref != "" && EnabledFor(patterns, provider, id) {
			n++
		}
	}
	return n
}

// anyEnabled reports whether patterns leave any of refs enabled.
func anyEnabled(patterns, refs []string) bool {
	return countEnabled(patterns, refs) > 0
}

// dedup keeps the first occurrence of each entry, dropping blanks.
func dedup(list []string) []string {
	out := make([]string, 0, len(list))
	seen := make(map[string]bool, len(list))
	for _, item := range list {
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}
