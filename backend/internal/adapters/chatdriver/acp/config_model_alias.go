package acp

import (
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// resolveLegacyModelChoice translates a CLI-facing model alias into the exact
// opaque value an ACP agent advertised. Cursor's CLI lists aliases such as
// composer-2.5-fast and gpt-5.5-medium while its legacy ACP selector includes
// those settings as parameters; session/set_model accepts only the latter.
// Exact provider values always win, and an alias is accepted only when it
// identifies one advertised choice unambiguously.
func resolveLegacyModelChoice(choices []ports.ChatConfigOptionChoice, requested string) (string, bool) {
	if choiceOffered(choices, requested) {
		return requested, true
	}

	matched := ""
	for _, choice := range choices {
		aliases, parameterized := parameterizedModelAliases(choice.Value)
		if !parameterized {
			continue
		}
		matches := false
		for _, alias := range aliases {
			if alias == requested {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		if matched != "" {
			return "", false
		}
		matched = choice.Value
	}
	return matched, matched != ""
}

func parameterizedModelAliases(value string) ([]string, bool) {
	open := strings.IndexByte(value, '[')
	if open <= 0 || !strings.HasSuffix(value, "]") {
		return nil, false
	}
	base := value[:open]
	body := value[open+1 : len(value)-1]
	if body == "" {
		if base == "default" {
			return []string{"auto"}, true
		}
		return nil, false
	}
	if strings.HasPrefix(base, "grok-") {
		base = "cursor-" + base
	}
	effort := ""
	fast := false
	fastSet := false
	thinking := false
	thinkingSet := false
	parameterized := false
	params := strings.Split(body, ",")
	for _, param := range params {
		key, raw, found := strings.Cut(strings.TrimSpace(param), "=")
		key = strings.TrimSpace(key)
		raw = strings.TrimSpace(raw)
		if !found || key == "" || raw == "" {
			return nil, false
		}
		switch key {
		case "reasoning", "effort", "reasoning_effort":
			if effort != "" && effort != raw {
				return nil, false
			}
			effort = raw
			parameterized = true
		case "fast":
			parsed := false
			switch raw {
			case "true":
				parsed = true
			case "false":
			default:
				return nil, false
			}
			if fastSet && fast != parsed {
				return nil, false
			}
			fast = parsed
			fastSet = true
			parameterized = true
		case "thinking":
			parsed := false
			switch raw {
			case "true":
				parsed = true
			case "false":
			default:
				return nil, false
			}
			if thinkingSet && thinking != parsed {
				return nil, false
			}
			thinking = parsed
			thinkingSet = true
			parameterized = true
		case "context":
			// Cursor does not include the context window in its CLI alias. If
			// multiple advertised values differ only by context, the caller's
			// ambiguity check still rejects the alias.
			parameterized = true
		default:
			// Never derive an alias while silently discarding a provider-owned
			// semantic parameter. New Cursor parameters must be mapped here
			// deliberately before their opaque values can be selected by alias.
			return nil, false
		}
	}
	if !parameterized {
		return nil, false
	}
	fastSuffix := ""
	if fast {
		fastSuffix = "-fast"
	}
	if !thinking {
		if effort != "" {
			base += "-" + effort
		}
		return []string{base + fastSuffix}, true
	}
	if effort == "" {
		return []string{base + "-thinking" + fastSuffix}, true
	}
	// Cursor has shipped both thinking-effort and effort-thinking alias orders
	// across Claude model families. Both retain the same advertised semantics;
	// the caller still rejects any alias shared by multiple advertised choices.
	return []string{
		base + "-thinking-" + effort + fastSuffix,
		base + "-" + effort + "-thinking" + fastSuffix,
	}, true
}
