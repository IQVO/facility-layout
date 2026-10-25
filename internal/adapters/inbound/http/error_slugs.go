package http

// ErrorSlug pairs one typed sentinel error with the stable problem-type slug
// the REST adapter reports for it (the last path segment of the RFC 7807
// "type" URI).
type ErrorSlug struct {
	Err  error
	Slug string
}

// ErrorSlugs lists every typed error this adapter maps to a problem slug, in
// the order errorCategories declares them. The MCP adapter keeps its own
// table of the same slugs (ADR-0033) and a test in that package compares the
// two one-for-one through this function; nothing at runtime depends on it, so
// the two adapters stay independent.
func ErrorSlugs() []ErrorSlug {
	out := make([]ErrorSlug, 0, len(errorCategories))
	for _, c := range errorCategories {
		out = append(out, ErrorSlug{Err: c.err, Slug: c.problem.slug})
	}
	return out
}
