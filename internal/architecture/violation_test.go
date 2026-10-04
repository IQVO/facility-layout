package architecture

import (
	"fmt"
	"reflect"
	"strings"
)

// archViolation renders an architecture-rule failure for an LLM (or human) reader: WHAT broke,
// WHY the rule exists and the exact FIX. Sensor output is a prompt: a bare "rule failed" forces
// the agent to guess, a remediation message lets it self-correct.
//
// This file is MANAGED by warehouse-harness-template (tools/migrate_v3.py); do not edit it in a
// service repo. kind is "dependency" or "contents"; rule is the rule description or package;
// details is whatever the checker reported.
func archViolation(kind, rule string, details any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ARCHITECTURE VIOLATION (%s rule)\n", kind)
	fmt.Fprintf(&b, "  RULE:    %s\n", rule)
	fmt.Fprintf(&b, "  DETAILS: %+v\n", failing(details))
	b.WriteString("  WHY:     hexagonal dependency direction keeps the domain pure and replaceable: domain imports " +
		"nothing; application imports domain + ports; adapters import application and domain; only cmd/ wires the " +
		"layers. Bounded contexts talk through events and ports, never through direct imports of each other.\n")
	b.WriteString("  FIX:     move the offending import behind a port in internal/application/ports (implemented by " +
		"an adapter), or move the code to the layer that is allowed to depend on it. Do NOT add an exception, edit " +
		"this rule or loosen the test; if the rule itself is wrong, record an ADR first " +
		"(.claude/skills/how-to-write-an-adr/SKILL.md).\n")
	return b.String()
}

// failing keeps only the verifications that did NOT pass (arch-go reports every package checked,
// passing ones included, which buries the actual offender).
func failing(details any) any {
	rv := reflect.ValueOf(details)
	if rv.Kind() != reflect.Slice {
		return details
	}
	var out []any
	for i := 0; i < rv.Len(); i++ {
		e := rv.Index(i)
		f := e
		for f.Kind() == reflect.Pointer || f.Kind() == reflect.Interface {
			f = f.Elem()
		}
		if f.Kind() == reflect.Struct {
			if p := f.FieldByName("Passes"); p.IsValid() && p.Kind() == reflect.Bool && p.Bool() {
				continue
			}
		}
		out = append(out, e.Interface())
	}
	return out
}
