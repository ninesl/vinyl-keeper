package values

// PageParam wraps a parameter name in braces for path patterns
func PageParam(name string) string {
	return "{" + name + "}"
}
