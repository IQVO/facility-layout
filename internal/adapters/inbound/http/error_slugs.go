package http

// ErrorSlug pairs one typed sentinel error with the stable problem-type slug
// the REST adapter reports for it (the last path segment of the RFC 7807
// "type" URI) and the HTTP status it answers with.
type ErrorSlug struct {
	Err    error
	Slug   string
	Status int
}

// ErrorSlugs lists every typed error this adapter maps to a problem slug, in
// the order errorCategories declares them. The MCP adapter keeps its own
// table of the same slugs (ADR-0033) and the A2A adapter its own table of
// slugs and statuses (ADR-0043); a test in each package compares its table
// with this one one-for-one through this function. Nothing at runtime depends
// on it, so the adapters stay independent.
func ErrorSlugs() []ErrorSlug {
	out := make([]ErrorSlug, 0, len(errorCategories))
	for _, c := range errorCategories {
		out = append(out, ErrorSlug{Err: c.err, Slug: c.problem.slug, Status: c.status})
	}
	return out
}
