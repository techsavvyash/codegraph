package main

import "testing"

// TestWriteKeywordRegex covers the read-only guardrail's keyword matcher in
// isolation (no Neo4j needed). It must reject genuine write/DDL clauses and
// must not false-positive on identifiers that merely start with a keyword
// (e.g. a `createdAt` property/alias, which is common on MENTIONS edges).
func TestWriteKeywordRegex(t *testing.T) {
	cases := []struct {
		name      string
		query     string
		wantMatch bool
	}{
		{"plain create", "CREATE (n:Foo)", true},
		{"lowercase create", "create (n:Foo)", true},
		{"merge", "MERGE (n:Foo {id: 1})", true},
		{"delete", "MATCH (n) DELETE n", true},
		{"detach delete", "MATCH (n) DETACH DELETE n", true},
		{"set clause", "MATCH (n) SET n.x = 1", true},
		{"remove clause", "MATCH (n) REMOVE n.x", true},
		{"drop index", "DROP INDEX foo", true},
		{"foreach", "FOREACH (x IN [1,2] | CREATE (:Y))", true},
		{"load csv", "LOAD CSV FROM 'file:///x.csv' AS row RETURN row", true},
		{"call subquery with create", "CALL { CREATE (n:Foo) } RETURN 1", true},

		{"createdAt property read", "MATCH (r:MENTIONS) RETURN r.createdAt AS createdAt", false},
		{"createdAt alias order by", "MATCH ()-[r:MENTIONS]->() RETURN r.createdAt AS linkedAt ORDER BY r.createdAt DESC", false},
		{"identifier prefixed with Set", "MATCH (n) RETURN n.Settings AS s", false},
		{"identifier prefixed with Drop", "MATCH (n) RETURN n.DropdownState AS s", false},
		{"identifier prefixed with Remove", "MATCH (n) RETURN n.RemovealCandidate AS s", false},
		{"plain read query", "MATCH (f:Function) WHERE f.name = 'x' RETURN f.name LIMIT 1", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := writeKeywordRegex.MatchString(stripCypherComments(tc.query))
			if got != tc.wantMatch {
				t.Errorf("writeKeywordRegex.MatchString(%q) = %v, want %v", tc.query, got, tc.wantMatch)
			}
		})
	}
}
