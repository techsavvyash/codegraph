package resolvers

import (
	"context"
	"fmt"

	"github.com/context-maximiser/code-graph/pkg/neo4j"
	"github.com/graphql-go/graphql"
	dbtype "github.com/neo4j/neo4j-go-driver/v5/neo4j/dbtype"
)

// SymbolResolver handles symbol-related queries
type SymbolResolver struct {
	client *neo4j.Client
}

// NewSymbolResolver creates a new symbol resolver
func NewSymbolResolver(client *neo4j.Client) *SymbolResolver {
	return &SymbolResolver{client: client}
}

// SearchSymbols searches for symbols by name or pattern
func (r *SymbolResolver) SearchSymbols(p graphql.ResolveParams) (interface{}, error) {
	ctx := context.Background()
	query := p.Args["query"].(string)
	limit, _ := p.Args["limit"].(int)

	cypher := `
		MATCH (sym:Symbol)
		WHERE sym.name CONTAINS $query OR sym.displayName CONTAINS $query
		RETURN sym
		ORDER BY sym.name
		LIMIT $limit
	`

	params := map[string]interface{}{
		"query": query,
		"limit": limit,
	}

	result, err := r.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("failed to search symbols: %w", err)
	}

	var symbols []map[string]interface{}
	for _, record := range result {
		recordMap := record.AsMap()

		if symNode, ok := recordMap["sym"].(dbtype.Node); ok {
			props := symNode.Props

			symbol := map[string]interface{}{
				"scipSymbol":    props["symbol"],
				"name":          props["name"],
				"kind":          props["kind"],
				"displayName":   props["displayName"],
				"filePath":      props["filePath"],
				"serviceName":   props["serviceName"],
				"startLine":     props["startLine"],
				"endLine":       props["endLine"],
				"signature":     props["signature"],
				"documentation": props["documentation"],
			}

			symbols = append(symbols, symbol)
		}
	}

	return symbols, nil
}
