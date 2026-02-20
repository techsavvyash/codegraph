#!/bin/bash
# Regenerate the SCIP fixture. Run from pkg/indexer/static/.
set -e

TMPDIR=$(mktemp -d)
trap "rm -rf $TMPDIR" EXIT

cat > "$TMPDIR/go.mod" << 'EOF'
module example.com/fixture

go 1.21
EOF

cat > "$TMPDIR/lib.go" << 'EOF'
package main

func Greet(name string) string { return "Hello, " + name }
EOF

cat > "$TMPDIR/main.go" << 'EOF'
package main

import "fmt"

func main() {
	msg := Greet("world")
	fmt.Println(msg)
}
EOF

cd "$TMPDIR"
scip-go --output "$TMPDIR/index.scip" --module-name "example.com/fixture"
cp "$TMPDIR/index.scip" "$(dirname "$0")/tiny_project.scip"
echo "Fixture regenerated: $(wc -c < "$(dirname "$0")/tiny_project.scip") bytes"
