package static

import (
	"context"
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/context-maximiser/code-graph/pkg/models"
	"github.com/context-maximiser/code-graph/pkg/neo4j"
	"github.com/context-maximiser/code-graph/pkg/search"
)

// StaticIndexer indexes Go source code into the graph database
type StaticIndexer struct {
	client           *neo4j.Client
	serviceName      string
	version          string
	repoURL          string
	packageMap       map[string]*models.Module // Cache for package/module nodes
	symbolMap        map[string]string         // Cache for symbol -> node ID mapping
	embeddingService search.EmbeddingService   // Optional embedding service
	vectorSearch     *search.VectorSearchManager
}

// NewStaticIndexer creates a new static indexer
func NewStaticIndexer(client *neo4j.Client, serviceName, version, repoURL string) *StaticIndexer {
	return &StaticIndexer{
		client:      client,
		serviceName: serviceName,
		version:     version,
		repoURL:     repoURL,
		packageMap:  make(map[string]*models.Module),
		symbolMap:   make(map[string]string),
		// Embedding service will be set later if needed
		embeddingService: nil,
		vectorSearch:     nil,
	}
}

// SetEmbeddingService sets the embedding service for automatic embedding generation
func (si *StaticIndexer) SetEmbeddingService(embeddingService search.EmbeddingService) {
	si.embeddingService = embeddingService
	if embeddingService != nil {
		si.vectorSearch = search.NewVectorSearchManager(si.client)
	}
}

// generateEmbeddingForNode generates and updates embedding for a newly created node
func (si *StaticIndexer) generateEmbeddingForNode(ctx context.Context, nodeID string, nodeType string, textParts ...string) {
	if si.embeddingService == nil || si.vectorSearch == nil {
		return // Skip if no embedding service configured
	}

	// Build text for embedding
	text := strings.Join(textParts, " | ")
	if text == "" {
		text = fmt.Sprintf("%s node", nodeType)
	}

	// Generate embedding
	embedding, err := si.embeddingService.GenerateEmbedding(ctx, text)
	if err != nil {
		log.Printf("Warning: failed to generate embedding for %s node %s: %v", nodeType, nodeID, err)
		return
	}

	// Update node with embedding
	err = si.vectorSearch.UpdateNodeEmbedding(ctx, nodeID, embedding)
	if err != nil {
		log.Printf("Warning: failed to update embedding for %s node %s: %v", nodeType, nodeID, err)
	}
}

// IndexProject indexes an entire Go project
func (si *StaticIndexer) IndexProject(ctx context.Context, rootPath string) error {
	log.Printf("Starting to index project at %s", rootPath)
	
	// Create or update the service node
	serviceID, err := si.createServiceNode(ctx)
	if err != nil {
		return fmt.Errorf("failed to create service node: %w", err)
	}
	log.Printf("Created service node with ID: %s", serviceID)

	// Walk the directory tree and index all Go files
	err = filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip vendor, .git, and other directories
		if d.IsDir() && shouldSkipDir(d.Name()) {
			return filepath.SkipDir
		}

		// Only process .go files
		if !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			log.Printf("Indexing file: %s", path)
			if err := si.indexFile(ctx, path, serviceID); err != nil {
				log.Printf("Warning: failed to index file %s: %v", path, err)
				// Continue with other files instead of failing completely
			}
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to walk directory: %w", err)
	}

	log.Printf("Successfully indexed project %s", si.serviceName)
	return nil
}

// IndexProjectIncremental performs incremental indexing by only updating changed files
func (si *StaticIndexer) IndexProjectIncremental(ctx context.Context, rootPath string) error {
	log.Printf("Starting incremental indexing of project at %s", rootPath)

	// Create or update the service node
	serviceID, err := si.createServiceNode(ctx)
	if err != nil {
		return fmt.Errorf("failed to create service node: %w", err)
	}
	log.Printf("Updated service node with ID: %s", serviceID)

	// Get existing file hashes from the database
	existingFiles, err := si.getExistingFileHashes(ctx)
	if err != nil {
		return fmt.Errorf("failed to get existing file hashes: %w", err)
	}
	log.Printf("Found %d existing files in database", len(existingFiles))

	// Track current files to detect deletions
	currentFiles := make(map[string]bool)

	// Walk the directory tree and index changed files
	err = filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip vendor, .git, and other directories
		if d.IsDir() && shouldSkipDir(d.Name()) {
			return filepath.SkipDir
		}

		// Only process .go files
		if !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			currentFiles[path] = true

			// Calculate current file hash
			currentHash, err := si.calculateFileHash(path)
			if err != nil {
				log.Printf("Warning: failed to calculate hash for %s: %v", path, err)
				return nil
			}

			// Check if file has changed
			existingHash, exists := existingFiles[path]
			if !exists || existingHash != currentHash {
				log.Printf("Indexing changed file: %s (new: %t)", path, !exists)
				if err := si.indexFileIncremental(ctx, path, serviceID, currentHash); err != nil {
					log.Printf("Warning: failed to index file %s: %v", path, err)
				}
			} else {
				log.Printf("Skipping unchanged file: %s", path)
			}
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to walk directory: %w", err)
	}

	// Clean up deleted files
	deletedCount := 0
	for filePath := range existingFiles {
		if !currentFiles[filePath] {
			log.Printf("Removing deleted file: %s", filePath)
			if err := si.removeFileNodes(ctx, filePath); err != nil {
				log.Printf("Warning: failed to remove file %s: %v", filePath, err)
			} else {
				deletedCount++
			}
		}
	}

	log.Printf("Successfully completed incremental indexing for %s (removed %d deleted files)",
		si.serviceName, deletedCount)
	return nil
}

// createServiceNode creates the service node in the graph
func (si *StaticIndexer) createServiceNode(ctx context.Context) (string, error) {
	serviceProps := map[string]any{
		"name":          si.serviceName,
		"language":      "Go",
		"version":       si.version,
		"repositoryUrl": si.repoURL,
		"createdAt":     time.Now().UTC().Unix(),
		"updatedAt":     time.Now().UTC().Unix(),
	}

	return si.client.MergeNode(ctx, []string{"Service"}, 
		map[string]any{"name": si.serviceName}, serviceProps)
}

// indexFile indexes a single Go source file
func (si *StaticIndexer) indexFile(ctx context.Context, filePath string, serviceID string) error {
	// Parse the file
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("failed to parse file %s: %w", filePath, err)
	}

	// Calculate file hash
	fileHash, err := si.calculateFileHash(filePath)
	if err != nil {
		return fmt.Errorf("failed to calculate file hash: %w", err)
	}

	// Create file node
	fileProps := map[string]any{
		"path":         filePath,
		"absolutePath": filePath,
		"language":     "Go",
		"hash":         fileHash,
		"lineCount":    fset.Position(node.End()).Line,
		"createdAt":    time.Now().UTC().Unix(),
		"updatedAt":    time.Now().UTC().Unix(),
	}

	fileID, err := si.client.MergeNode(ctx, []string{"File"}, 
		map[string]any{"path": filePath}, fileProps)
	if err != nil {
		return fmt.Errorf("failed to create file node: %w", err)
	}

	// Link file to service
	_, err = si.client.CreateRelationship(ctx, serviceID, fileID, "CONTAINS", nil)
	if err != nil {
		return fmt.Errorf("failed to link file to service: %w", err)
	}

	// Index the package/module
	packageName := node.Name.Name
	packageFQN := si.getPackageFQN(filePath, packageName)
	
	moduleID, err := si.getOrCreateModule(ctx, packageName, packageFQN, fileID)
	if err != nil {
		return fmt.Errorf("failed to create module node: %w", err)
	}

	// Create a visitor to traverse the AST
	visitor := &astVisitor{
		indexer:   si,
		ctx:       ctx,
		fileID:    fileID,
		moduleID:  moduleID,
		filePath:  filePath,
		fset:      fset,
		packageName: packageName,
	}

	// Visit all nodes in the AST
	ast.Walk(visitor, node)

	return nil
}

// astVisitor implements ast.Visitor to traverse and index AST nodes
type astVisitor struct {
	indexer     *StaticIndexer
	ctx         context.Context
	fileID      string
	moduleID    string
	filePath    string
	fset        *token.FileSet
	packageName string
	currentClass string // Track current class/struct for methods
}

// Visit implements ast.Visitor
func (v *astVisitor) Visit(node ast.Node) ast.Visitor {
	if node == nil {
		return nil
	}

	switch n := node.(type) {
	case *ast.FuncDecl:
		v.indexFunction(n)
	case *ast.TypeSpec:
		v.indexType(n)
	case *ast.GenDecl:
		v.indexGenDecl(n)
	case *ast.InterfaceType:
		v.indexInterface(n)
	}

	return v
}

// indexFunction indexes a function declaration
func (v *astVisitor) indexFunction(fn *ast.FuncDecl) {
	if fn.Name == nil {
		return
	}

	startPos := v.fset.Position(fn.Pos())
	endPos := v.fset.Position(fn.End())

	// Determine if this is a method or function
	isMethod := fn.Recv != nil
	var parentID string

	if isMethod {
		// Try to find the receiver type and link to it
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			if recv := fn.Recv.List[0]; recv.Type != nil {
				// Extract receiver type name
				var recvTypeName string
				switch t := recv.Type.(type) {
				case *ast.Ident:
					recvTypeName = t.Name
				case *ast.StarExpr:
					if ident, ok := t.X.(*ast.Ident); ok {
						recvTypeName = ident.Name
					}
				}
				v.currentClass = recvTypeName
				// TODO: Link to the actual struct/type node
				parentID = v.moduleID // For now, link to module
			}
		}
	} else {
		parentID = v.moduleID
	}

	// Build function signature
	signature := v.buildFunctionSignature(fn)

	// Extract return type
	returnType := ""
	if fn.Type.Results != nil {
		returnType = v.extractTypeString(fn.Type.Results)
	}

	// Check if function is exported
	isExported := ast.IsExported(fn.Name.Name)

	// Create function/method node with enhanced location metadata
	funcProps := map[string]any{
		"name":        fn.Name.Name,
		"signature":   signature,
		"returnType":  returnType,
		"filePath":    v.filePath,
		"startLine":   startPos.Line,
		"endLine":     endPos.Line,
		"startColumn": startPos.Column,
		"endColumn":   endPos.Column,
		"startByte":   v.fset.Position(fn.Pos()).Offset,
		"endByte":     v.fset.Position(fn.End()).Offset,
		"linesOfCode": endPos.Line - startPos.Line + 1,
		"isExported":  isExported,
		"isAsync":     false, // Go doesn't have async functions like JS
		"complexity":  1,     // TODO: Calculate cyclomatic complexity
		"docstring":   v.extractDocstring(fn.Doc),
		"createdAt":   time.Now().UTC().Unix(),
		"updatedAt":   time.Now().UTC().Unix(),
	}

	var labels []string
	if isMethod {
		labels = []string{"Method"}
		funcProps["accessModifier"] = "public" // Go methods are public if capitalized
		funcProps["isStatic"] = false
	} else {
		labels = []string{"Function"}
	}

	funcID, err := v.indexer.client.MergeNode(v.ctx, labels,
		map[string]any{"signature": signature, "filePath": v.filePath}, funcProps)
	if err != nil {
		log.Printf("Failed to create function node %s: %v", fn.Name.Name, err)
		return
	}

	// Generate embedding for the function
	nodeType := "Function"
	if isMethod {
		nodeType = "Method"
	}
	v.indexer.generateEmbeddingForNode(v.ctx, funcID, nodeType,
		fn.Name.Name, signature, v.extractDocstring(fn.Doc))

	// Link to parent (module or class)
	if parentID != "" {
		_, err = v.indexer.client.CreateRelationship(v.ctx, parentID, funcID, "CONTAINS", nil)
		if err != nil {
			log.Printf("Failed to link function to parent: %v", err)
		}
	}

	// Create symbol for the function
	v.createSymbol(fn.Name.Name, "Function", funcID, signature)

	// Index parameters
	if fn.Type.Params != nil {
		for i, param := range fn.Type.Params.List {
			for _, name := range param.Names {
				v.indexParameter(name, param, i, funcID)
			}
		}
	}

	// TODO: Index function calls and references within the function body
}

// indexType indexes type declarations (structs, aliases, etc.)
func (v *astVisitor) indexType(typeSpec *ast.TypeSpec) {
	if typeSpec.Name == nil {
		return
	}

	startPos := v.fset.Position(typeSpec.Pos())
	endPos := v.fset.Position(typeSpec.End())

	// Determine the type of declaration
	switch t := typeSpec.Type.(type) {
	case *ast.StructType:
		v.indexStruct(typeSpec.Name.Name, t, startPos, endPos)
	case *ast.InterfaceType:
		v.indexInterfaceType(typeSpec.Name.Name, t, startPos, endPos)
	}
}

// indexStruct indexes a struct type
func (v *astVisitor) indexStruct(name string, structType *ast.StructType, startPos, endPos token.Position) {
	fqn := fmt.Sprintf("%s.%s", v.packageName, name)
	
	classProps := map[string]any{
		"name":           name,
		"fqn":            fqn,
		"filePath":       v.filePath,
		"startLine":      startPos.Line,
		"endLine":        endPos.Line,
		"startColumn":    startPos.Column,
		"endColumn":      endPos.Column,
		"startByte":      startPos.Offset,
		"endByte":        endPos.Offset,
		"linesOfCode":    endPos.Line - startPos.Line + 1,
		"accessModifier": "public", // Go structs are public if capitalized
		"isAbstract":     false,
		"isInterface":    false,
		"docstring":      "", // TODO: Extract docstring
		"createdAt":      time.Now().UTC().Unix(),
		"updatedAt":      time.Now().UTC().Unix(),
	}

	classID, err := v.indexer.client.MergeNode(v.ctx, []string{"Class"},
		map[string]any{"fqn": fqn}, classProps)
	if err != nil {
		log.Printf("Failed to create struct node %s: %v", name, err)
		return
	}

	// Generate embedding for the class
	v.indexer.generateEmbeddingForNode(v.ctx, classID, "Class", name, fqn)

	// Link to module
	_, err = v.indexer.client.CreateRelationship(v.ctx, v.moduleID, classID, "CONTAINS", nil)
	if err != nil {
		log.Printf("Failed to link struct to module: %v", err)
	}

	// Create symbol for the struct
	v.createSymbol(name, "Type", classID, fqn)

	// Index fields
	if structType.Fields != nil {
		for _, field := range structType.Fields.List {
			for _, fieldName := range field.Names {
				v.indexField(fieldName, field, classID)
			}
		}
	}
}

// indexInterfaceType indexes an interface type
func (v *astVisitor) indexInterfaceType(name string, interfaceType *ast.InterfaceType, startPos, endPos token.Position) {
	fqn := fmt.Sprintf("%s.%s", v.packageName, name)
	
	interfaceProps := map[string]any{
		"name":        name,
		"fqn":         fqn,
		"filePath":    v.filePath,
		"startLine":   startPos.Line,
		"endLine":     endPos.Line,
		"startColumn": startPos.Column,
		"endColumn":   endPos.Column,
		"startByte":   startPos.Offset,
		"endByte":     endPos.Offset,
		"linesOfCode": endPos.Line - startPos.Line + 1,
		"docstring":   "", // TODO: Extract docstring
		"createdAt":   time.Now().UTC().Unix(),
		"updatedAt":   time.Now().UTC().Unix(),
	}

	interfaceID, err := v.indexer.client.MergeNode(v.ctx, []string{"Interface"}, 
		map[string]any{"fqn": fqn}, interfaceProps)
	if err != nil {
		log.Printf("Failed to create interface node %s: %v", name, err)
		return
	}

	// Link to module
	_, err = v.indexer.client.CreateRelationship(v.ctx, v.moduleID, interfaceID, "CONTAINS", nil)
	if err != nil {
		log.Printf("Failed to link interface to module: %v", err)
	}

	// Create symbol for the interface
	v.createSymbol(name, "Interface", interfaceID, fqn)
}

// indexGenDecl indexes general declarations (vars, consts, types)
func (v *astVisitor) indexGenDecl(gen *ast.GenDecl) {
	for _, spec := range gen.Specs {
		switch s := spec.(type) {
		case *ast.ValueSpec:
			v.indexValueSpec(s, gen.Tok)
		}
	}
}

// indexValueSpec indexes variable or constant declarations
func (v *astVisitor) indexValueSpec(spec *ast.ValueSpec, tok token.Token) {
	for _, name := range spec.Names {
		if name.Name == "_" { // Skip blank identifier
			continue
		}

		startPos := v.fset.Position(name.Pos())
		endPos := v.fset.Position(name.End())

		// Determine variable type
		varType := ""
		if spec.Type != nil {
			varType = v.extractTypeString(&ast.FieldList{List: []*ast.Field{{Type: spec.Type}}})
		}

		// Determine scope and if it's a constant
		scope := "package"
		isConstant := tok == token.CONST
		if !ast.IsExported(name.Name) {
			scope = "private"
		}

		varProps := map[string]any{
			"name":         name.Name,
			"type":         varType,
			"scope":        scope,
			"filePath":     v.filePath,
			"startLine":    startPos.Line,
			"endLine":      endPos.Line,
			"isConstant":   isConstant,
			"initialValue": "", // TODO: Extract initial value
			"createdAt":    time.Now().UTC().Unix(),
			"updatedAt":    time.Now().UTC().Unix(),
		}

		varID, err := v.indexer.client.MergeNode(v.ctx, []string{"Variable"}, 
			map[string]any{"name": name.Name, "filePath": v.filePath}, varProps)
		if err != nil {
			log.Printf("Failed to create variable node %s: %v", name.Name, err)
			continue
		}

		// Link to module
		_, err = v.indexer.client.CreateRelationship(v.ctx, v.moduleID, varID, "CONTAINS", nil)
		if err != nil {
			log.Printf("Failed to link variable to module: %v", err)
		}

		// Create symbol for the variable
		symbolKind := "Variable"
		if isConstant {
			symbolKind = "Constant"
		}
		v.createSymbol(name.Name, symbolKind, varID, fmt.Sprintf("%s.%s", v.packageName, name.Name))
	}
}

// indexParameter indexes function parameters
func (v *astVisitor) indexParameter(name *ast.Ident, param *ast.Field, index int, funcID string) {
	paramType := v.extractTypeString(&ast.FieldList{List: []*ast.Field{param}})

	paramProps := map[string]any{
		"name":         name.Name,
		"type":         paramType,
		"index":        index,
		"isOptional":   false, // Go doesn't have optional parameters
		"defaultValue": "",
		"createdAt":    time.Now().UTC().Unix(),
		"updatedAt":    time.Now().UTC().Unix(),
	}

	paramID, err := v.indexer.client.MergeNode(v.ctx, []string{"Parameter"}, 
		map[string]any{"name": name.Name, "filePath": v.filePath, "index": index}, paramProps)
	if err != nil {
		log.Printf("Failed to create parameter node %s: %v", name.Name, err)
		return
	}

	// Link to function
	_, err = v.indexer.client.CreateRelationship(v.ctx, funcID, paramID, "CONTAINS", nil)
	if err != nil {
		log.Printf("Failed to link parameter to function: %v", err)
	}

	// Create symbol for the parameter
	v.createSymbol(name.Name, "Parameter", paramID, "")
}

// indexField indexes struct fields
func (v *astVisitor) indexField(name *ast.Ident, field *ast.Field, classID string) {
	startPos := v.fset.Position(name.Pos())
	endPos := v.fset.Position(name.End())

	fieldType := v.extractTypeString(&ast.FieldList{List: []*ast.Field{field}})

	varProps := map[string]any{
		"name":         name.Name,
		"type":         fieldType,
		"scope":        "instance",
		"filePath":     v.filePath,
		"startLine":    startPos.Line,
		"endLine":      endPos.Line,
		"isConstant":   false,
		"initialValue": "",
		"createdAt":    time.Now().UTC().Unix(),
		"updatedAt":    time.Now().UTC().Unix(),
	}

	fieldID, err := v.indexer.client.MergeNode(v.ctx, []string{"Variable"}, 
		map[string]any{"name": name.Name, "filePath": v.filePath}, varProps)
	if err != nil {
		log.Printf("Failed to create field node %s: %v", name.Name, err)
		return
	}

	// Link to class
	_, err = v.indexer.client.CreateRelationship(v.ctx, classID, fieldID, "CONTAINS", nil)
	if err != nil {
		log.Printf("Failed to link field to class: %v", err)
	}

	// Create symbol for the field
	v.createSymbol(name.Name, "Field", fieldID, "")
}

// Helper methods
func (v *astVisitor) createSymbol(name, kind, nodeID, descriptor string) {
	// Create SCIP symbol
	scipSymbol := models.NewGoSCIPSymbol(v.packageName, v.indexer.version, descriptor)

	symbolProps := map[string]any{
		"symbol":        scipSymbol.String(),
		"kind":          kind,
		"displayName":   name,
		"documentation": "",
		"createdAt":     time.Now().UTC().Unix(),
		"updatedAt":     time.Now().UTC().Unix(),
	}

	symbolID, err := v.indexer.client.MergeNode(v.ctx, []string{"Symbol"}, 
		map[string]any{"symbol": scipSymbol.String()}, symbolProps)
	if err != nil {
		log.Printf("Failed to create symbol for %s: %v", name, err)
		return
	}

	// Create DEFINES relationship
	_, err = v.indexer.client.CreateRelationship(v.ctx, nodeID, symbolID, "DEFINES", 
		map[string]any{"isExported": ast.IsExported(name)})
	if err != nil {
		log.Printf("Failed to create DEFINES relationship for %s: %v", name, err)
	}

	// Cache the symbol mapping
	v.indexer.symbolMap[scipSymbol.String()] = nodeID
}

func (v *astVisitor) buildFunctionSignature(fn *ast.FuncDecl) string {
	var parts []string
	
	parts = append(parts, fn.Name.Name)
	parts = append(parts, "(")
	
	if fn.Type.Params != nil {
		var params []string
		for _, param := range fn.Type.Params.List {
			paramType := v.extractTypeString(&ast.FieldList{List: []*ast.Field{param}})
			for _, name := range param.Names {
				params = append(params, fmt.Sprintf("%s %s", name.Name, paramType))
			}
		}
		parts = append(parts, strings.Join(params, ", "))
	}
	
	parts = append(parts, ")")
	
	if fn.Type.Results != nil {
		parts = append(parts, " ")
		parts = append(parts, v.extractTypeString(fn.Type.Results))
	}
	
	return strings.Join(parts, "")
}

func (v *astVisitor) extractTypeString(fieldList *ast.FieldList) string {
	if fieldList == nil || len(fieldList.List) == 0 {
		return ""
	}
	
	// Simple type extraction - can be enhanced
	field := fieldList.List[0]
	if field.Type != nil {
		switch t := field.Type.(type) {
		case *ast.Ident:
			return t.Name
		case *ast.StarExpr:
			if ident, ok := t.X.(*ast.Ident); ok {
				return "*" + ident.Name
			}
		case *ast.SelectorExpr:
			if pkg, ok := t.X.(*ast.Ident); ok {
				return pkg.Name + "." + t.Sel.Name
			}
		}
	}
	
	return "unknown"
}

func (v *astVisitor) extractDocstring(commentGroup *ast.CommentGroup) string {
	if commentGroup == nil {
		return ""
	}
	
	var parts []string
	for _, comment := range commentGroup.List {
		text := strings.TrimPrefix(comment.Text, "//")
		text = strings.TrimPrefix(text, "/*")
		text = strings.TrimSuffix(text, "*/")
		text = strings.TrimSpace(text)
		if text != "" {
			parts = append(parts, text)
		}
	}
	
	return strings.Join(parts, " ")
}

// getOrCreateModule gets or creates a module node for a package
func (si *StaticIndexer) getOrCreateModule(ctx context.Context, packageName, fqn, fileID string) (string, error) {
	// Check cache first
	if module, exists := si.packageMap[fqn]; exists {
		// Link file to existing module
		_, err := si.client.CreateRelationship(ctx, module.ID, fileID, "CONTAINS", nil)
		return module.ID, err
	}

	// Create new module
	moduleProps := map[string]any{
		"name":       packageName,
		"fqn":        fqn,
		"type":       "package",
		"isExported": true, // Go packages are generally exported
		"createdAt":  time.Now().UTC().Unix(),
		"updatedAt":  time.Now().UTC().Unix(),
	}

	moduleID, err := si.client.MergeNode(ctx, []string{"Module"}, 
		map[string]any{"fqn": fqn}, moduleProps)
	if err != nil {
		return "", fmt.Errorf("failed to create module: %w", err)
	}

	// Cache the module
	si.packageMap[fqn] = &models.Module{
		BaseNode: models.BaseNode{ID: moduleID},
		Name:     packageName,
		FQN:      fqn,
	}

	// Link file to module
	_, err = si.client.CreateRelationship(ctx, moduleID, fileID, "CONTAINS", nil)
	if err != nil {
		return "", fmt.Errorf("failed to link file to module: %w", err)
	}

	return moduleID, nil
}

// Helper functions
func (si *StaticIndexer) getPackageFQN(filePath, packageName string) string {
	// Simple FQN generation - can be enhanced with proper module detection
	return fmt.Sprintf("%s/%s", si.serviceName, packageName)
}

func (si *StaticIndexer) calculateFileHash(filePath string) (string, error) {
	// Read file contents and calculate hash
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	hash := sha256.Sum256(content)
	return fmt.Sprintf("%x", hash), nil
}

// getExistingFileHashes retrieves existing file paths and their hashes from the database
func (si *StaticIndexer) getExistingFileHashes(ctx context.Context) (map[string]string, error) {
	query := `
		MATCH (s:Service {name: $serviceName})-[:CONTAINS]->(f:File)
		RETURN f.path as path, f.hash as hash
	`

	params := map[string]any{"serviceName": si.serviceName}
	results, err := si.client.ExecuteQuery(ctx, query, params)
	if err != nil {
		return nil, fmt.Errorf("failed to query existing files: %w", err)
	}

	fileHashes := make(map[string]string)
	for _, record := range results {
		recordMap := record.AsMap()
		path, ok := recordMap["path"].(string)
		if !ok {
			continue
		}
		hash, ok := recordMap["hash"].(string)
		if !ok {
			continue
		}
		fileHashes[path] = hash
	}

	return fileHashes, nil
}

// indexFileIncremental indexes a single file with incremental logic
func (si *StaticIndexer) indexFileIncremental(ctx context.Context, filePath, serviceID, fileHash string) error {
	// First, remove existing nodes for this file to avoid duplicates
	if err := si.removeFileNodes(ctx, filePath); err != nil {
		log.Printf("Warning: failed to remove existing nodes for %s: %v", filePath, err)
	}

	// Now index the file normally with the new hash
	return si.indexFileWithHash(ctx, filePath, serviceID, fileHash)
}

// indexFileWithHash is like indexFile but accepts a pre-calculated hash
func (si *StaticIndexer) indexFileWithHash(ctx context.Context, filePath, serviceID, fileHash string) error {
	// Parse the file
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("failed to parse file %s: %w", filePath, err)
	}

	// Create file node with the provided hash
	fileProps := map[string]any{
		"path":         filePath,
		"absolutePath": filePath,
		"language":     "Go",
		"hash":         fileHash,
		"lineCount":    fset.Position(node.End()).Line,
		"createdAt":    time.Now().UTC().Unix(),
		"updatedAt":    time.Now().UTC().Unix(),
	}

	fileID, err := si.client.MergeNode(ctx, []string{"File"},
		map[string]any{"path": filePath}, fileProps)
	if err != nil {
		return fmt.Errorf("failed to create file node: %w", err)
	}

	// Link file to service
	_, err = si.client.CreateRelationship(ctx, serviceID, fileID, "CONTAINS", nil)
	if err != nil {
		return fmt.Errorf("failed to link file to service: %w", err)
	}

	// Index the package/module
	packageName := node.Name.Name
	packageFQN := si.getPackageFQN(filePath, packageName)

	moduleID, err := si.getOrCreateModule(ctx, packageName, packageFQN, fileID)
	if err != nil {
		return fmt.Errorf("failed to create module node: %w", err)
	}

	// Create a visitor to traverse the AST
	visitor := &astVisitor{
		indexer:   si,
		ctx:       ctx,
		fileID:    fileID,
		moduleID:  moduleID,
		filePath:  filePath,
		fset:      fset,
		packageName: packageName,
	}

	// Visit all nodes in the AST
	ast.Walk(visitor, node)

	return nil
}

// removeFileNodes removes all nodes associated with a file
func (si *StaticIndexer) removeFileNodes(ctx context.Context, filePath string) error {
	query := `
		MATCH (f:File {path: $filePath})
		OPTIONAL MATCH (f)-[:CONTAINS|DEFINES|DECLARES|CALLS|BELONGS_TO*]-(related)
		DETACH DELETE f, related
	`

	params := map[string]any{"filePath": filePath}
	_, err := si.client.ExecuteQuery(ctx, query, params)
	if err != nil {
		return fmt.Errorf("failed to remove file nodes for %s: %w", filePath, err)
	}

	log.Printf("Removed nodes for file: %s", filePath)
	return nil
}

func shouldSkipDir(dirName string) bool {
	skipDirs := []string{
		"vendor", ".git", ".github", "node_modules", ".vscode",
		"bin", "build", "dist", "tmp", ".idea",
	}
	
	for _, skip := range skipDirs {
		if dirName == skip {
			return true
		}
	}
	
	return false
}

// indexInterface indexes an interface declaration
func (v *astVisitor) indexInterface(interfaceType *ast.InterfaceType) {
	// This method is called when visiting InterfaceType nodes directly
	// The actual interface indexing is handled in indexInterfaceType
}