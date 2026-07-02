package workspace

import "quietforge/storage"

type FileNode struct {
	Path     string
	Hash     string
	Symbols  []storage.WorkspaceSymbolRow
	Edges    []storage.WorkspaceEdgeRow
}
