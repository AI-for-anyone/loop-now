package runturn

// ToolFiles tells sink, when it records tool targets, which files a file tool (read, edit, write) works on. Only the
// paths go out, never contents or other arguments; empty paths are left out.
func ToolFiles(sink any, id, name string, paths ...string) {
	var given []string
	for _, p := range paths {
		if p != "" {
			given = append(given, p)
		}
	}
	if s, ok := sink.(interface {
		ToolFiles(id, name string, paths []string)
	}); ok && len(given) > 0 {
		s.ToolFiles(id, name, given)
	}
}

// ToolQuery tells sink, when it records tool targets, the pattern or query of a search tool.
func ToolQuery(sink any, id, name, query string) {
	if s, ok := sink.(interface{ ToolQuery(id, name, query string) }); ok && query != "" {
		s.ToolQuery(id, name, query)
	}
}
