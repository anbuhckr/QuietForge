package workspace

import (
	"fmt"
	"strings"
	"time"

	"quietforge/storage"

	sitter "github.com/smacker/go-tree-sitter"
)

func ExtractFacts(workspace, path string, data []byte, tree *sitter.Tree) ([]storage.WorkspaceSymbolRow, []storage.WorkspaceEdgeRow) {
	var symbols []storage.WorkspaceSymbolRow
	var edges []storage.WorkspaceEdgeRow

	root := tree.RootNode()
	now := time.Now().Unix()

	var activeScopes []string
	seenEdges := make(map[string]bool)

	getCallTarget := func(n *sitter.Node) string {
		if n == nil {
			return ""
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			c := n.Child(i)
			t := c.Type()
			if t == "identifier" || t == "selector_expression" || t == "member_expression" || t == "scoped_identifier" {
				return c.Content(data)
			}
		}
		return ""
	}

	var walk func(node *sitter.Node)
	walk = func(node *sitter.Node) {
		t := node.Type()
		lt := strings.ToLower(t)

		// 1. Symbols (Func, Struct, Class)
		isDef := strings.Contains(lt, "function") ||
			strings.Contains(lt, "method") ||
			strings.Contains(lt, "class") ||
			strings.Contains(lt, "struct") ||
			strings.Contains(lt, "interface") ||
			strings.Contains(lt, "declaration") ||
			strings.Contains(lt, "definition")

		var currentDefName string

		if isDef {
			nameNode := node.ChildByFieldName("name")
			if nameNode == nil {
				for i := 0; i < int(node.ChildCount()); i++ {
					child := node.Child(i)
					if child.Type() == "identifier" || child.Type() == "property_identifier" || child.Type() == "type_identifier" {
						nameNode = child
						break
					}
				}
			}

			if nameNode != nil {
				name := nameNode.Content(data)
				if name != "" && len(name) < 100 {
					symType := "function"
					if strings.Contains(lt, "class") {
						symType = "class"
					}
					if strings.Contains(lt, "struct") {
						symType = "struct"
					}
					if strings.Contains(lt, "interface") {
						symType = "interface"
					}
					if strings.Contains(lt, "method") {
						symType = "method"
					}

					id := fmt.Sprintf("sym-%s-%s-%d", path, name, node.StartPoint().Row)
					symbols = append(symbols, storage.WorkspaceSymbolRow{
						ID: id, Workspace: workspace, Path: path, Name: name, Type: symType,
						LineStart: int(node.StartPoint().Row) + 1, LineEnd: int(node.EndPoint().Row) + 1,
						UpdatedAt: now,
					})

					if symType == "function" || symType == "method" {
						currentDefName = name
						activeScopes = append(activeScopes, currentDefName)
					}
				}
			}
		}

		// 2. Edges (Imports)
		isImport := strings.Contains(lt, "import") || strings.Contains(lt, "require")
		if isImport {
			for i := 0; i < int(node.ChildCount()); i++ {
				child := node.Child(i)
				if strings.Contains(child.Type(), "string") {
					target := strings.Trim(child.Content(data), `"\'`+"`")
					if target != "" {
						edgeKey := fmt.Sprintf("import:%s:%s", path, target)
						if !seenEdges[edgeKey] {
							seenEdges[edgeKey] = true
							id := fmt.Sprintf("edge-imp-%s-%d", target, node.StartPoint().Row)
							edges = append(edges, storage.WorkspaceEdgeRow{
								ID: id, Workspace: workspace, SourcePath: path, TargetPath: target, EdgeType: "import",
							})
						}
					}
				}
			}
		}

		// 3. Edges (Calls)
		isCall := lt == "call_expression" || lt == "invocation"
		if isCall && len(activeScopes) > 0 {
			target := getCallTarget(node)
			if target != "" {
				source := activeScopes[len(activeScopes)-1]
				edgeKey := fmt.Sprintf("calls:%s:%s", source, target)
				if !seenEdges[edgeKey] {
					seenEdges[edgeKey] = true
					edges = append(edges, storage.WorkspaceEdgeRow{
						ID:         fmt.Sprintf("edge-call-%s-%s-%d", source, target, node.StartPoint().Row),
						Workspace:  workspace,
						SourcePath: source,
						TargetPath: target,
						EdgeType:   "calls",
					})
				}
			}
		}

		// 4. Edges (References)
		isRef := t == "identifier" || t == "type_identifier"
		isDefName := false
		if node.Parent() != nil {
			pt := node.Parent().Type()
			plt := strings.ToLower(pt)
			if strings.Contains(plt, "function") || strings.Contains(plt, "method") || strings.Contains(plt, "class") || strings.Contains(plt, "struct") || strings.Contains(plt, "interface") || strings.Contains(plt, "declaration") {
				if node.Parent().ChildByFieldName("name") == node {
					isDefName = true
				}
			}
		}

		if isRef && !isDefName {
			target := node.Content(data)
			if len(target) > 2 {
				edgeKey := fmt.Sprintf("refs:%s:%s", path, target)
				if !seenEdges[edgeKey] {
					seenEdges[edgeKey] = true
					edges = append(edges, storage.WorkspaceEdgeRow{
						ID:         fmt.Sprintf("edge-ref-%s-%d", target, node.StartPoint().Row),
						Workspace:  workspace,
						SourcePath: path,
						TargetPath: target,
						EdgeType:   "references",
					})
				}
			}
		}

		// Recurse
		for i := 0; i < int(node.ChildCount()); i++ {
			walk(node.Child(i))
		}

		// Pop scope
		if currentDefName != "" {
			activeScopes = activeScopes[:len(activeScopes)-1]
		}
	}

	if root != nil {
		walk(root)
	}

	return symbols, edges
}
