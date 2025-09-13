# Neo4j Schema Definition

This document defines the complete Neo4j schema for the Code Intelligence Platform, based on the Code Property Graph (CPG) model.

## Node Labels and Properties

### Core Code Nodes

#### `:Service`
Represents a top-level microservice or application component.

**Properties:**
- `name: string` - Service identifier
- `language: string` - Primary programming language
- `version: string` - Version identifier
- `repositoryUrl: string` - Git repository URL
- `createdAt: datetime` - When indexed
- `updatedAt: datetime` - Last update time

**Indexes:**
- `CREATE INDEX service_name_idx FOR (s:Service) ON (s.name)`

#### `:File`
Represents a source code file.

**Properties:**
- `path: string` - Relative file path from service root
- `absolutePath: string` - Full filesystem path
- `language: string` - File programming language
- `size: int` - File size in bytes
- `lineCount: int` - Total lines of code
- `hash: string` - Content hash for change detection
- `createdAt: datetime`
- `updatedAt: datetime`

**Indexes:**
- `CREATE INDEX file_path_idx FOR (f:File) ON (f.path)`
- `CREATE INDEX file_hash_idx FOR (f:File) ON (f.hash)`

#### `:Module`
Represents a logical code grouping (package, namespace, module).

**Properties:**
- `name: string` - Module name
- `fqn: string` - Fully qualified name
- `type: string` - Module type (package, namespace, etc.)
- `isExported: boolean` - Whether module is publicly accessible

**Indexes:**
- `CREATE INDEX module_fqn_idx FOR (m:Module) ON (m.fqn)`

#### `:Class`
Represents an object-oriented class definition.

**Properties:**
- `name: string` - Class name
- `fqn: string` - Fully qualified name
- `filePath: string` - Containing file path
- `startLine: int` - Starting line number
- `endLine: int` - Ending line number
- `accessModifier: string` - public, private, protected, etc.
- `isAbstract: boolean`
- `isInterface: boolean`
- `docstring: string` - Associated documentation

**Indexes:**
- `CREATE INDEX class_name_idx FOR (c:Class) ON (c.name)`
- `CREATE INDEX class_fqn_idx FOR (c:Class) ON (c.fqn)`

#### `:Interface`
Represents an interface definition.

**Properties:**
- `name: string`
- `fqn: string`
- `filePath: string`
- `startLine: int`
- `endLine: int`
- `docstring: string`

**Indexes:**
- `CREATE INDEX interface_fqn_idx FOR (i:Interface) ON (i.fqn)`

#### `:Function`
Represents a standalone function or static method.

**Properties:**
- `name: string` - Function name
- `signature: string` - Full function signature
- `returnType: string` - Return type
- `filePath: string` - Containing file
- `startLine: int`
- `endLine: int`
- `isExported: boolean` - Whether function is public
- `isAsync: boolean` - Whether function is asynchronous
- `complexity: int` - Cyclomatic complexity
- `docstring: string`

**Indexes:**
- `CREATE INDEX function_name_idx FOR (f:Function) ON (f.name)`
- `CREATE INDEX function_signature_idx FOR (f:Function) ON (f.signature)`

#### `:Method`
Represents an instance method belonging to a class.

**Properties:**
- `name: string`
- `signature: string`
- `returnType: string`
- `accessModifier: string`
- `filePath: string`
- `startLine: int`
- `endLine: int`
- `isStatic: boolean`
- `isAbstract: boolean`
- `isOverride: boolean`
- `complexity: int`
- `docstring: string`

**Indexes:**
- `CREATE INDEX method_name_idx FOR (m:Method) ON (m.name)`

#### `:Variable`
Represents a variable declaration.

**Properties:**
- `name: string` - Variable name
- `type: string` - Variable type
- `scope: string` - local, instance, class, global
- `filePath: string`
- `startLine: int`
- `endLine: int`
- `isConstant: boolean`
- `initialValue: string` - Initial value if literal

**Indexes:**
- `CREATE INDEX variable_name_idx FOR (v:Variable) ON (v.name)`

#### `:Parameter`
Represents a function/method parameter.

**Properties:**
- `name: string` - Parameter name
- `type: string` - Parameter type
- `index: int` - Position in parameter list
- `isOptional: boolean`
- `defaultValue: string` - Default value if any

### Semantic Nodes

#### `:Symbol`
Canonical representation of a code symbol using SCIP format.

**Properties:**
- `symbol: string` - SCIP-formatted symbol identifier
- `kind: string` - Symbol kind (class, method, variable, etc.)
- `displayName: string` - Human-readable name
- `documentation: string` - Associated documentation

**Constraints:**
- `CREATE CONSTRAINT symbol_unique FOR (s:Symbol) REQUIRE s.symbol IS UNIQUE`

**Indexes:**
- `CREATE INDEX symbol_kind_idx FOR (s:Symbol) ON (s.kind)`

### API and Integration Nodes

#### `:APIRoute`
Represents an exposed API endpoint.

**Properties:**
- `path: string` - API endpoint path
- `method: string` - HTTP method (GET, POST, etc.)
- `protocol: string` - Protocol type (REST, gRPC, GraphQL)
- `description: string` - Endpoint description
- `isDeprecated: boolean`
- `version: string` - API version

**Indexes:**
- `CREATE INDEX api_route_path_idx FOR (r:APIRoute) ON (r.path)`
- `CREATE INDEX api_route_method_idx FOR (r:APIRoute) ON (r.method)`

### Documentation Nodes

#### `:Comment`
Represents code comments and docstrings.

**Properties:**
- `text: string` - Comment text
- `type: string` - Comment type (line, block, docstring)
- `filePath: string`
- `startLine: int`
- `endLine: int`
- `isDocstring: boolean`

#### `:Document`
Represents technical or business documents.

**Properties:**
- `title: string` - Document title
- `type: string` - Document type (PRD, RFC, spec, etc.)
- `sourceUrl: string` - Source location
- `content: string` - Document content
- `createdAt: datetime`
- `updatedAt: datetime`

**Indexes:**
- `CREATE INDEX document_title_idx FOR (d:Document) ON (d.title)`
- `CREATE INDEX document_type_idx FOR (d:Document) ON (d.type)`

#### `:Feature`
Represents a specific feature or capability described in documents.

**Properties:**
- `name: string` - Feature name
- `description: string` - Feature description
- `status: string` - Implementation status
- `priority: string` - Priority level
- `tags: list<string>` - Associated tags

**Indexes:**
- `CREATE INDEX feature_name_idx FOR (f:Feature) ON (f.name)`

## Relationship Types

### Structural Relationships

#### `:CONTAINS`
Represents hierarchical containment (AST-like structure).

**Properties:**
- `order: int` - Order within container (for ordered relationships)

**Examples:**
- `(:Service)-[:CONTAINS]->(:File)`
- `(:File)-[:CONTAINS]->(:Class)`
- `(:Class)-[:CONTAINS]->(:Method)`
- `(:Method)-[:CONTAINS]->(:Variable)`

#### `:DEFINES`
Represents symbol definitions.

**Properties:**
- `isExported: boolean` - Whether definition is publicly accessible

**Examples:**
- `(:Function)-[:DEFINES]->(:Symbol)`
- `(:Class)-[:DEFINES]->(:Symbol)`

#### `:REFERENCES`
Represents symbol usage sites.

**Properties:**
- `isDefinition: boolean` - Whether this is the defining reference
- `line: int` - Line number of reference
- `column: int` - Column number of reference

**Examples:**
- `(:Variable)-[:REFERENCES]->(:Symbol)`
- `(:Method)-[:REFERENCES]->(:Symbol)`

### Behavioral Relationships

#### `:CALLS`
Represents function/method invocations.

**Properties:**
- `isDynamic: boolean` - Whether call is dynamically resolved
- `line: int` - Line number of call
- `isRecursive: boolean` - Whether call is recursive

**Examples:**
- `(:Method)-[:CALLS]->(:Function)`
- `(:Function)-[:CALLS]->(:Method)`

#### `:FLOWS_TO`
Represents data flow dependencies.

**Properties:**
- `path: list<string>` - Data flow path
- `flowType: string` - Type of flow (direct, indirect, conditional)

**Examples:**
- `(:Parameter)-[:FLOWS_TO]->(:Variable)`
- `(:Variable)-[:FLOWS_TO]->(:Parameter)`

#### `:NEXT_EXECUTION`
Represents control flow between statements.

**Properties:**
- `isConditional: boolean` - Whether execution is conditional
- `condition: string` - Condition for execution (if conditional)

### Object-Oriented Relationships

#### `:INHERITS_FROM`
Represents class inheritance.

**Examples:**
- `(:Class)-[:INHERITS_FROM]->(:Class)`

#### `:IMPLEMENTS`
Represents interface implementation or feature realization.

**Examples:**
- `(:Class)-[:IMPLEMENTS]->(:Interface)`
- `(:Function)-[:IMPLEMENTS]->(:Feature)`

### API Relationships

#### `:EXPOSES_API`
Connects code handlers to API endpoints.

**Examples:**
- `(:Method)-[:EXPOSES_API]->(:APIRoute)`
- `(:Function)-[:EXPOSES_API]->(:APIRoute)`

#### `:CALLS_API`
Represents API calls between services.

**Properties:**
- `timeout: int` - Call timeout in milliseconds
- `retryCount: int` - Number of retries

**Examples:**
- `(:Method)-[:CALLS_API]->(:APIRoute)`

### Service Relationships

#### `:DEPENDS_ON`
Represents dependencies between services or modules.

**Properties:**
- `version: string` - Dependency version
- `isDirect: boolean` - Whether dependency is direct

**Examples:**
- `(:Service)-[:DEPENDS_ON]->(:Service)`
- `(:Module)-[:DEPENDS_ON]->(:Module)`

### Documentation Relationships

#### `:DESCRIBES`
Connects documents to features or code elements.

**Examples:**
- `(:Document)-[:DESCRIBES]->(:Feature)`
- `(:Comment)-[:DESCRIBES]->(:Function)`

#### `:MENTIONS`
Represents references in documentation.

**Properties:**
- `context: string` - Context of the mention

**Examples:**
- `(:Document)-[:MENTIONS]->(:Symbol)`
- `(:Feature)-[:MENTIONS]->(:Class)`

## Schema Creation Script

```cypher
// Create constraints for unique identifiers
CREATE CONSTRAINT symbol_unique FOR (s:Symbol) REQUIRE s.symbol IS UNIQUE;
CREATE CONSTRAINT service_name_unique FOR (s:Service) REQUIRE s.name IS UNIQUE;

// Create indexes for performance
CREATE INDEX service_name_idx FOR (s:Service) ON (s.name);
CREATE INDEX file_path_idx FOR (f:File) ON (f.path);
CREATE INDEX file_hash_idx FOR (f:File) ON (f.hash);
CREATE INDEX class_name_idx FOR (c:Class) ON (c.name);
CREATE INDEX class_fqn_idx FOR (c:Class) ON (c.fqn);
CREATE INDEX function_name_idx FOR (f:Function) ON (f.name);
CREATE INDEX function_signature_idx FOR (f:Function) ON (f.signature);
CREATE INDEX method_name_idx FOR (m:Method) ON (m.name);
CREATE INDEX variable_name_idx FOR (v:Variable) ON (v.name);
CREATE INDEX symbol_kind_idx FOR (s:Symbol) ON (s.kind);
CREATE INDEX api_route_path_idx FOR (r:APIRoute) ON (r.path);
CREATE INDEX document_title_idx FOR (d:Document) ON (d.title);
CREATE INDEX feature_name_idx FOR (f:Feature) ON (f.name);

// Create composite indexes for common queries
CREATE INDEX file_service_path_idx FOR (f:File) ON (f.serviceName, f.path);
CREATE INDEX symbol_service_idx FOR (s:Symbol) ON (s.serviceName, s.kind);
```

## Migration Strategy

1. **Initial Setup**: Create all constraints and indexes
2. **Backward Compatibility**: Add new properties as optional
3. **Schema Evolution**: Use property prefixes for versioning when needed
4. **Data Migration**: Implement migration scripts for schema changes