package permission

import (
	"path/filepath"
	"strings"
)

type Action string

const (
	Allow Action = "allow"
	Ask   Action = "ask"
	Deny  Action = "deny"
)

type PermissionRule struct {
	Permission string
	Pattern    string
	Action     Action
}

type Evaluation struct {
	Action Action
	Rule   *PermissionRule
}

type Ruleset []PermissionRule

var (
	AllowAll = Ruleset{
		{
			Permission: "*",
			Pattern:    "*",
			Action:     Allow,
		},
	}

	DenyAll = Ruleset{
		{
			Permission: "*",
			Pattern:    "*",
			Action:     Deny,
		},
	}
)

func match(pattern, value string) bool {
	ok, err := filepath.Match(pattern, value)
	return err == nil && ok
}

func Evaluate(permissionName string, pattern string, ruleset Ruleset) Evaluation {
	if len(ruleset) == 0 {
		return Evaluation{Action: Allow}
	}

	var matched *PermissionRule

	for i := range ruleset {
		rule := &ruleset[i]

		if match(rule.Permission, permissionName) &&
			match(rule.Pattern, pattern) {
			matched = rule // last matching rule wins
		}
	}

	if matched != nil {
		return Evaluation{
			Action: matched.Action,
			Rule:   matched,
		}
	}

	return Evaluation{Action: Allow}
}

func Merge(rulesets ...Ruleset) Ruleset {
	var result Ruleset

	for _, rs := range rulesets {
		result = append(result, rs...)
	}

	return result
}

func FromConfig(config map[string]any) Ruleset {
	var rules Ruleset

	for perm, value := range config {

		switch v := value.(type) {

		case string:
			switch {
			case v == "allow":
				rules = append(rules, PermissionRule{
					Permission: perm,
					Pattern:    "*",
					Action:     Allow,
				})

			case v == "ask":
				rules = append(rules, PermissionRule{
					Permission: perm,
					Pattern:    "*",
					Action:     Ask,
				})

			case v == "deny":
				rules = append(rules, PermissionRule{
					Permission: perm,
					Pattern:    "*",
					Action:     Deny,
				})

			case strings.HasPrefix(v, "allow:"):
				rules = append(rules, PermissionRule{
					Permission: perm,
					Pattern:    strings.TrimPrefix(v, "allow:"),
					Action:     Allow,
				})

			case strings.HasPrefix(v, "deny:"):
				rules = append(rules, PermissionRule{
					Permission: perm,
					Pattern:    strings.TrimPrefix(v, "deny:"),
					Action:     Deny,
				})
			}

		case map[string]any:
			for subPattern, actionValue := range v {

				actionStr, ok := actionValue.(string)
				if !ok {
					continue
				}

				switch Action(actionStr) {
				case Allow, Ask, Deny:
					rules = append(rules, PermissionRule{
						Permission: perm,
						Pattern:    subPattern,
						Action:     Action(actionStr),
					})
				}
			}

		case map[string]string:
			for subPattern, actionStr := range v {

				switch Action(actionStr) {
				case Allow, Ask, Deny:
					rules = append(rules, PermissionRule{
						Permission: perm,
						Pattern:    subPattern,
						Action:     Action(actionStr),
					})
				}
			}
		}
	}

	return rules
}

func Disabled(permissions map[string]struct{}, ruleset Ruleset) map[string]struct{} {
	result := make(map[string]struct{})

	for p := range permissions {
		ev := Evaluate(p, "*", ruleset)
		if ev.Action == Deny {
			result[p] = struct{}{}
		}
	}

	return result
}