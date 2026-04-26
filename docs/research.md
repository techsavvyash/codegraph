# **Architectural Blueprint for an LLM-Powered Code Intelligence Platform**

## **Executive Summary**

This report provides a comprehensive architectural blueprint for a custom code intelligence platform tailored for a microservices environment. It addresses the core requirements of creating a rich, contextual code index to empower a Large Language Model (LLM) for advanced coding tasks. We present a dual-pipeline indexing architecture that efficiently handles both actively developed and static services, leveraging tree-sitter for incremental parsing and the Source Code Intelligence Protocol (SCIP) for baseline indexing. A third pipeline is introduced to ingest and analyze unstructured documents, such as Product Requirement Documents (PRDs) and technical specifications, using LLM-based information extraction techniques. The cornerstone of this architecture is a unified Code Property Graph (CPG) model stored in Neo4j, which captures not only the syntactic structure and data flow of the code but also its connection to the business and technical requirements that drive its existence. The report details the expanded graph schema, the ingestion pipelines, and a suite of powerful Cypher query patterns designed to provide the LLM with unprecedented contextual awareness, from code navigation to tracing features from specification to implementation.

## **I. The Unified Code Graph: A Data Model for Deep Context**

This section defines the target data structure in Neo4j. The model's richness is the foundation upon which all subsequent intelligence is built. Simply storing an Abstract Syntax Tree (AST) is insufficient for the deep contextual understanding an LLM requires. To enable sophisticated reasoning, the model must capture the semantics of execution, data flow, and the conceptual knowledge contained in documentation.

### **1.1. Foundational Principles: Beyond the AST to a Code Property Graph (CPG)**

The initial impulse when representing code is to model its syntactic structure. However, this approach is fundamentally limited and fails to capture the dynamic behavior and semantic relationships that are critical for genuine code understanding.

#### **Limitations of a Pure AST Model**

An Abstract Syntax Tree (AST) is a tree representation of the abstract syntactic structure of source code, typically generated during the parsing phase of a compiler.1 While it is a fundamental data structure for code analysis, storing a direct representation of the AST in a graph database, while an improvement over raw text, is insufficient for the goals of this project. An AST can effectively answer questions about syntax, such as "what is the structure of this function?" or "what are the arguments to this class constructor?". However, it lacks explicit information about the order of execution (control flow) or the movement of data between variables and functions (data flow). Consequently, queries against a pure AST model are confined to structural analysis and cannot readily answer more profound questions like "what is the execution path that leads to this error condition?" or "if I change this variable, what downstream calculations are affected?". Moving beyond simple text-based searching requires a data model that encodes these deeper semantic relationships.2

#### **Introducing the Code Property Graph (CPG)**

The Code Property Graph (CPG) is a more advanced data structure that addresses the shortcomings of the AST by unifying multiple classic program representations into a single, queryable graph.4 It was originally conceived for vulnerability discovery, as identifying security flaws often requires a joint understanding of syntax, control flow, and data dependencies.5 A CPG overlays the following critical information onto a base AST:

* **Control Flow Graph (CFG):** The CFG represents the possible order of execution of program statements. In the graph model, this is represented by edges connecting statements that can execute sequentially. This is indispensable for understanding program logic, identifying unreachable code, and tracing execution paths that an LLM would need to reason about program behavior.5  
* **Program Dependence Graph (PDG):** The PDG represents data and control dependencies between program elements. A data dependency exists when one statement's output (e.g., assigning a value to a variable) is used as an input by another statement. A control dependency exists when the execution of a statement depends on the outcome of a predicate (e.g., an if condition). These relationships are vital for tasks like data lineage tracking, impact analysis, and taint analysis.5

Code snippet

graph TD  
    subgraph "Code Property Graph (CPG)"  
        subgraph "Abstract Syntax Tree (AST)"  
            direction TB  
            A \--\> B\["Parameter (x)"\];  
            A \--\> C{"IfStmt (x \> 0)"};  
            C \--\> D\["Assign (y \= x \* 2)"\];  
            A \--\> E;  
        end  
        subgraph "Control Flow Graph (CFG)"  
            C \-- "true" \--\> D;  
            D \-- " " \--\> E;  
            C \-- "false" \--\> E;  
        end  
        subgraph "Program Dependence Graph (PDG)"  
            B \-.-\> D;  
            D \-.-\> E;  
        end  
    end  
    style A fill:\#f9f,stroke:\#333,stroke-width:2px  
    style B fill:\#f9f,stroke:\#333,stroke-width:2px  
    style C fill:\#f9f,stroke:\#333,stroke-width:2px  
    style D fill:\#f9f,stroke:\#333,stroke-width:2px  
    style E fill:\#f9f,stroke:\#333,stroke-width:2px  
    linkStyle 4 stroke:blue,stroke-width:2px,fill:none,stroke-dasharray: 5 5;  
    linkStyle 5 stroke:blue,stroke-width:2px,fill:none,stroke-dasharray: 5 5;  
    linkStyle 6 stroke:blue,stroke-width:2px,fill:none,stroke-dasharray: 5 5;  
    linkStyle 7 stroke:red,stroke-width:2px,fill:none,stroke-dasharray: 2 2;  
    linkStyle 8 stroke:red,stroke-width:2px,fill:none,stroke-dasharray: 2 2;

#### **Adopting a CPG-Inspired Model**

The proposed schema for this project will be a pragmatic, CPG-inspired model tailored for implementation in Neo4j and optimized for the specific contextual needs of an LLM. Rather than implementing a strict, academic CPG, this model will prioritize the relationships that provide the most value for code understanding and generation tasks. This includes a robust representation of the AST hierarchy, overlaid with explicit relationships for function calls, inheritance, implementations, and, crucially, data flow between key variables and control flow between statements. This approach ensures that the graph can be queried for expressive patterns involving full structural, type, and flow information, scaling to programs with millions of lines of code.2

### **1.2. Core Schema Definition for Neo4j**

The schema is designed to be language-agnostic at its core, with language-specific details stored as properties on nodes and relationships. This structure leverages the labeled-property graph model native to Neo4j, which consists of nodes (entities), relationships (connections), and properties (key-value pairs on both nodes and relationships).9

The following table serves as the definitive blueprint for the Neo4j database structure. It details every primary node label and relationship type, providing a clear guide for the implementation of the indexing pipelines.

| Element Type | Label/Type | Description | Key Properties | Example Cypher Pattern |
| :---- | :---- | :---- | :---- | :---- |
| **Node** | Service | Top-level container for a microservice, defining a major component boundary. | name: string, language: string, version: string, repositoryUrl: string | MERGE (s:Service {name: 'order-service'}) |
| **Node** | File | A single source code file within a service. | path: string, language: string | MATCH (f:File {path: '/src/main/java/com/example/Order.java'}) |
| **Node** | Module | A logical grouping of code like a package, namespace, or module. | name: string, fqn: string | MATCH (m:Module {name: 'com.example.api'}) |
| **Node** | Class | An object-oriented class definition. | name: string, fqn: string, filePath: string, startLine: int, endLine: int | MATCH (c:Class {name: 'OrderController'}) |
| **Node** | Interface | An interface definition. | name: string, fqn: string | MATCH (i:Interface {name: 'Payable'}) |
| **Node** | Function | A standalone function or a static method. | name: string, signature: string, returnType: string, isExported: boolean | MATCH (fn:Function {name: 'calculateTotal'}) |
| **Node** | Method | An instance method belonging to a class or other structure. | name: string, signature: string, returnType: string, accessModifier: string | MATCH (m:Method {name: 'addItem'}) |
| **Node** | Variable | A variable declaration (local, instance, or global). | name: string, type: string, scope: string | MATCH (v:Variable {name: 'orderItems'}) |
| **Node** | Parameter | A parameter of a function or method. | name: string, type: string, index: int | MATCH (p:Parameter {name: 'newItem', index: 0}) |
| **Node** | Symbol | A canonical node representing a unique definition, keyed by its SCIP symbol. | symbol: string, kind: string | MATCH (s:Symbol {symbol: $scipSymbol}) |
| **Node** | APIRoute | An exposed API endpoint (REST, gRPC, etc.). | path: string, method: string, protocol: string | MATCH (r:APIRoute {path: '/api/orders/{id}', method: 'GET'}) |
| **Node** | Comment | A code comment, linked to the code it describes. | text: string, isDocstring: boolean | MATCH (c:Comment) |
| **Node** | Document | A technical or business document (PRD, RFP, etc.). | title: string, type: string, sourceUrl: string | MATCH (d:Document {title: 'Q3 Feature Roadmap'}) |
| **Node** | Feature | A specific feature or capability described in a document. | name: string, description: string, status: string | MATCH (f:Feature {name: 'User Authentication'}) |
| **Relationship** | CONTAINS | Represents structural containment, forming the AST-like hierarchy. |  | (:File)--\>(:Class) |
| **Relationship** | CALLS | A function or method invokes another. | isDynamic: boolean, line: int | (:Method {name: 'placeOrder'})--\>(:Function {name: 'validatePayment'}) |
| **Relationship** | DEFINES | A scope (file, class) defines a symbol. |  | (:Class)--\>(:Symbol) |
| **Relationship** | REFERENCES | A usage site refers to a defined symbol. | isDefinition: boolean | (:Variable)--\>(:Symbol) |
| **Relationship** | INHERITS\_FROM | A class extends another class. |  | (:Dog)--\>(:Animal) |
| **Relationship** | IMPLEMENTS | A class implements an interface or a feature. |  | (:OrderService)--\>(:Transactional) or (:Function)--\>(:Feature) |
| **Relationship** | FLOWS\_TO | Data dependency: the value from the source node can flow to the target node. | path: list\<string\> | (:Parameter)--\>(:Variable) |
| **Relationship** | NEXT\_EXECUTION | Control flow: the target statement can execute immediately after the source. | isConditional: boolean | (:Statement)--\>(:Statement) |
| **Relationship** | EXPOSES\_API | A function or method is the handler for an API route. |  | (:Method {name: 'getOrderById'})--\>(:APIRoute) |
| **Relationship** | DEPENDS\_ON | A dependency between services or modules. | version: string | (:Service {name: 'A'})--\>(:Service {name: 'B'}) |
| **Relationship** | DESCRIBES | A document describes a feature or requirement. |  | (:Document)--\>(:Feature) |
| **Relationship** | MENTIONS | A document or feature mentions a code symbol. |  | (:Document)--\>(:Symbol) |

Code snippet

erDiagram  
    Service |

|--o{ File : CONTAINS  
    File |

|--o{ Class : CONTAINS  
    File |

|--o{ Function : CONTAINS  
    Class |

|--o{ Method : CONTAINS  
    Class |

|--o{ Variable : CONTAINS  
    Method |

|--o{ Parameter : CONTAINS  
    Method |

|--o{ Variable : CONTAINS  
    Class }o--|

| Interface : IMPLEMENTS  
    Class }o--|

| Class : INHERITS\_FROM  
    Method }o--|

| Function : CALLS  
    Method }o--|

| Method : CALLS  
    Function }o--|

| Function : CALLS  
    Method }o--|

| APIRoute : EXPOSES\_API  
    Variable }o--|

| Symbol : REFERENCES  
    Method }o--|

| Symbol : DEFINES  
    Document |

|--o{ Feature : DESCRIBES  
    Feature }o--|

| Symbol : MENTIONS  
    Function }o--|

| Feature : IMPLEMENTS

### **1.3. Modeling Cross-Service Interactions**

A disconnected graph for each service provides localized context but fails to capture the interconnected nature of a microservices architecture. The true analytical power of this system is unlocked by creating a unified graph that explicitly models interactions across service boundaries.

Code snippet

graph TD  
    subgraph OrderService  
        A\["Method: checkout()"\] \--"CALLS"--\> B((":Symbol\<br\>'...PaymentProcessor\#process().'"))  
    end  
    subgraph PaymentService  
        C\["Method: process()"\] \--"DEFINES"--\> B  
    end  
    subgraph APIGateway  
        D \--\> E("APIRoute: /orders")  
        E \--"EXPOSES\_API"--\> F\["Method: createOrder()"\]  
        F \--"CALLS\_API"--\> G("APIRoute: /payments")  
    end  
    subgraph PaymentService  
        G \--"EXPOSES\_API"--\> C  
    end

#### **The Role of SCIP Symbols**

The key to linking these disparate service graphs is a globally unique and stable identifier for every code definition. The SCIP protocol provides exactly this through its standardized symbol format.11 A SCIP symbol is a human-readable string that encodes the package manager, package name, version, and the fully qualified path to a specific definition (e.g., class, method).13 For example, the symbol

scip-python python PyYAML 6.0 yaml/dump(). uniquely identifies the dump function in version 6.0 of the PyYAML package. By adopting this format for the symbol property on our :Symbol nodes, we inherit a robust, battle-tested solution for global identification, obviating the need to invent a new, complex, and likely error-prone symbol resolution system.

#### **Stitching the Graph**

The :Symbol node acts as a global anchor point. The indexing process for each service will create :Symbol nodes for both the definitions it contains and the external symbols it references. When Service A calls a function in Service B, the graph models this interaction by connecting the call site in Service A to the canonical :Symbol node representing the function in Service B.

For instance, a call from checkout() in the order-service to process() in the payment-service would be modeled as follows:

1. **In order-service's graph:** (:Method {name: 'checkout'})--\>(:Symbol {symbol: 'scip-java maven com.example payment-service 1.2.0 com/example/PaymentProcessor\#process().'})  
2. **In payment-service's graph:** (:Method {name: 'process'})--\>(:Symbol {symbol: 'scip-java maven com.example payment-service 1.2.0 com/example/PaymentProcessor\#process().'})

Because both relationships point to the *same* :Symbol node (identified by its unique symbol property), a Cypher query can now traverse from the call site in one service directly to the definition in another, enabling seamless cross-repository navigation.

#### **Modeling API Calls**

Microservice interactions are not limited to shared library calls; they are predominantly network calls governed by API contracts. A pure code graph would miss this critical layer of abstraction. Therefore, the graph model must be extended to include first-class representations of these network boundaries. This is achieved with the :APIRoute node and the :CALLS\_API relationship.

The indexing process can be enhanced to parse API definition files, such as OpenAPI/Swagger specifications 14 or gRPC

.proto files 16, to create

:APIRoute nodes. The indexer can then identify code patterns corresponding to HTTP client or gRPC stub invocations and create :CALLS\_API relationships to the appropriate :APIRoute node in the target service's graph. This allows the LLM to reason about network boundaries, API contracts, potential latency issues, and data serialization formats, providing a far deeper level of context than code analysis alone can offer.

### **1.4. Integrating Unstructured Knowledge: Documents as First-Class Citizens**

Code does not exist in a vacuum; it is created to satisfy business requirements, technical specifications, and user stories. To provide an LLM with true end-to-end context, the knowledge graph must bridge the gap between these high-level documents and the low-level code that implements them.42 By modeling documents as first-class citizens in the graph, we create a holistic knowledge base that connects the "why" with the "how."

This is achieved by introducing new node labels and relationships to the schema:

* **:Document Node:** Represents a single source of unstructured or semi-structured information, such as a PRD, an RFP, a design document, or a markdown file. It holds metadata like the title, type, and source URL.  
* **:Feature Node:** Represents a specific, extractable concept or requirement from a document, such as "Implement OAuth 2.0 for user authentication" or "Optimize the checkout process." These nodes capture the core business logic and technical requirements.43  
* **:DESCRIBES Relationship:** A relationship from a :Document node to a :Feature node, indicating that the document specifies or details that particular feature.  
* **:IMPLEMENTS Relationship:** A relationship connecting a code element (like a :Function or :Class) to a :Feature node. This is the critical link that grounds a high-level requirement in the concrete source code.  
* **:MENTIONS Relationship:** A relationship from a :Document or :Feature to a :Symbol, used when a document explicitly references a specific code entity.

This extension transforms the code graph into a true knowledge graph, enabling queries that can trace a line of reasoning from a business requirement in a PRD all the way down to the specific functions and API endpoints that bring it to life.42

## **II. Architectural Patterns for Indexing a Microservices Ecosystem**

This section details the three distinct data pipelines required to populate and maintain the unified knowledge graph: a full, static indexing process for stable services, a real-time, incremental process for the service under active development, and a new pipeline for ingesting and structuring knowledge from documents.

### **2.1. Static Indexing Pipeline for Stable Services**

This pipeline is designed for the one-time, comprehensive indexing of a stable microservice or library. It is optimized for accuracy and batch processing throughput. The process is executed once for each version of a service that needs to be included in the knowledge graph.

Code snippet

graph TD  
    A \--\> B{"SCIP Indexer (e.g., scip-java)"};  
    B \--\> C\["index.scip file (Protobuf)"\];  
    C \--\> D;  
    D \-- "Batched Cypher (UNWIND \+ MERGE)" \--\> E;

#### **Step 1: SCIP Index Generation**

The foundation of the static pipeline is the generation of a high-fidelity code index using a language-specific SCIP indexer. Sourcegraph provides and maintains a suite of robust indexers for most major programming languages, including scip-java (for Java, Scala, and Kotlin) 18,

scip-python 13,

scip-typescript (for TypeScript and JavaScript) 21, and

scip-clang (for C/C++).22

These tools operate by integrating deeply with the language's compiler or type-checker. This allows them to produce a compiler-accurate index that correctly resolves symbols, understands types, and maps relationships like inheritance and implementation. The indexer is run against the service's source code, typically in a CI/CD environment, and produces a single index.scip file as its output. This file is a serialized Protocol Buffers (Protobuf) message, providing a compact, strongly-typed, and language-agnostic representation of the code intelligence data.11 For debugging or inspection, the binary content of this file can be decoded into a human-readable format using the

protoc command-line utility.23

#### **Step 2: Parsing the index.scip File**

The index.scip file is not a graph itself but a structured collection of data that describes the graph. It primarily contains three types of messages: Metadata, Document, and SymbolInformation.12 A

Document message contains the source code text and a list of Occurrence messages, which map ranges in the text to specific symbols. The SymbolInformation messages provide details about each symbol, such as its documentation and relationships to other symbols.

A custom ingestion service must be developed to process this file. This service will use standard Protobuf libraries for the target language (e.g., Go, Python, Java) to parse the index.scip file and load its contents into a structured, in-memory representation. This representation will serve as the source for the subsequent transformation and loading phase.

#### **Step 3: Transformation and Loading into Neo4j**

This step constitutes the Extract-Transform-Load (ETL) phase of the pipeline, where the parsed SCIP data is converted into Cypher statements and loaded into the Neo4j database.24 To perform this efficiently at scale, batching is essential.

* **Batching with UNWIND:** The most performant method for loading large datasets into Neo4j is to minimize the number of transactions and network round-trips. The UNWIND clause in Cypher is purpose-built for this. The ingestion service should group similar entities (e.g., all file nodes, all function nodes) into lists of maps (JSON objects). This list is then passed as a single parameter to a Cypher query. The UNWIND clause iterates over this list, creating a row for each element, which can then be processed by subsequent clauses in the same query. This technique allows for the creation or merging of thousands of nodes and relationships in a single transaction.26  
* **Idempotency with MERGE:** To ensure that the indexing process is repeatable and does not create duplicate data, all CREATE operations should be replaced with MERGE. The MERGE clause instructs Neo4j to find a node or relationship matching a given pattern and, if it does not exist, create it. This makes the entire loading process idempotent, which is critical for reliability and maintenance.25  
* Example Batch Loading Query (for Documents and Symbols):  
  The ingestion service would prepare a parameter $documents which is a list, where each element is a map containing a document's path and a list of symbols it defines.  
  Cypher  
  // $documents: \[{path: "/app/main.py", symbols: \[{symbol: "...", kind: "Method"},...\]},...\]  
  UNWIND $documents AS docData  
  MERGE (f:File {path: docData.path})  
  SET f.language \= 'python' // Set additional properties

  WITH f, docData.symbols AS symbols  
  UNWIND symbols AS symbolData  
  MERGE (s:Symbol {symbol: symbolData.symbol})  
  ON CREATE SET s.kind \= symbolData.kind

  // Create the structural relationship  
  MERGE (f)--\>(s)

This pattern is repeated for all entities and relationships described in the index.scip file, breaking the process into logical, manageable chunks (e.g., one query for all nodes, another for all CALLS relationships, etc.).

### **2.2. Incremental Indexing Pipeline for Active Development**

This pipeline is designed for the "hot" service currently being edited by a developer. It is optimized for extremely low latency to provide a near real-time feedback loop, similar to what an IDE provides. This process relies on tree-sitter's efficient incremental parsing capabilities.

Code snippet

graph TD  
    A \--\> B{"tree-sitter: tree.edit()"};  
    B \--\> C;  
    subgraph "AST-to-Cypher Translator"  
        direction LR  
        D(Old AST) \--\> F{"AST Diffing\<br\>(changed\_ranges)"};  
        C \--\> F;  
        F \--\> G;  
    end  
    G \--\> H;

#### **Step 1: Change Detection with tree-sitter**

The process begins in the developer's editor, which is equipped with a tree-sitter-aware plugin. The plugin maintains the current AST for any open file. When the developer types, the plugin does not trigger a full re-parse. Instead, it leverages tree-sitter's core incremental parsing feature.27

The plugin captures the edit (start position, old end position, new end position) and passes this information to the parser via the tree.edit() function. This function updates the tree's internal state to account for the change. The parser then efficiently re-parses only the affected portions of the code, reusing the unchanged nodes from the previous tree, and returns a new, updated AST.29 This entire process is fast enough to be run on every keystroke.31

#### **Step 2: Identifying Affected Regions**

After receiving the new AST, the crucial next step is to identify precisely which parts of the tree were structurally modified. This is accomplished by calling old\_tree.changed\_ranges(new\_tree). This function compares the old and new trees and returns a minimal set of text ranges that encompass all structural changes.29 These ranges are the input for the next stage, ensuring that the update process only considers the code that has actually changed, rather than performing a costly full-tree comparison.

#### **Step 3: AST Diffing and Graph Transaction Generation**

This is the most intricate and critical custom component of the architecture: the **AST-to-Cypher Translator**. This service receives the old AST, the new AST, and the list of changed ranges. Its responsibility is to perform a semantic diff of the subtrees within these ranges and generate a corresponding set of Cypher statements to apply the changes to the graph database. This process is analogous to the logic used in semantic diffing tools like diffsitter.34

The translator must analyze the nodes in the old and new subtrees to determine the nature of the edit:

* **Addition:** A node (e.g., a new function definition, a variable declaration) exists in the new tree but not the old. This translates into a series of MERGE statements to create the corresponding nodes (e.g., :Function, :Symbol) and relationships (e.g., :DEFINES, :CONTAINS) in the graph.  
* **Deletion:** A node exists in the old tree but not the new. This requires a MATCH query to find the corresponding node in the graph and a DETACH DELETE clause to remove it and all of its associated relationships atomically.37  
* **Modification:** A node's properties have changed (e.g., a variable is renamed, a function signature is altered). This translates to a MATCH query to find the node and a SET clause to update its properties.37

This component must be robust to transient syntax errors, which tree-sitter handles gracefully by creating distinct ERROR nodes in the AST.31 The translator should recognize these nodes and potentially hold off on generating updates until the syntax becomes valid again, or generate updates that mark the corresponding graph elements as being in an error state.

#### **Step 4: Atomic Updates to Neo4j**

The set of Cypher statements generated from a single code modification event (e.g., a file save) must be executed within a single database transaction. This ensures atomicity: either all the changes are applied successfully, or none are. This prevents the graph from entering an inconsistent state where, for example, a node has been deleted but relationships still point to it.

The architecture must also consider a hybrid update strategy. While the tree-sitter diffing approach is highly efficient for small, localized edits, it can become computationally expensive for very large changes, such as checking out a new git branch. The system should incorporate a heuristic: if the number of changed files or the total size of the changed\_ranges exceeds a predefined threshold, the incremental pipeline should be bypassed. Instead, the system should trigger a targeted run of the static indexing pipeline (using a file-level SCIP indexer) for only the affected files. This creates a resilient system that is optimized for both small, frequent edits and large, infrequent updates.

### **2.3. Document Indexing Pipeline: Extracting Knowledge with LLMs**

To populate the graph with knowledge from PRDs, RFPs, and other technical documents, a dedicated pipeline is required that can process unstructured text and convert it into structured graph elements.44 This pipeline leverages an LLM for its advanced natural language understanding capabilities.45

Code snippet

graph TD  
    A \--\> B{Text Chunking};  
    B \--\> C{LLM Information Extraction\<br\>(Entities & Relationships)};  
    C \--\> D;  
    D \--\> E{Mermaid Diagram Parsing (if present)};  
    E \-- Extracted Diagram Entities \--\> F;  
    D \-- Extracted Text Entities \--\> F;  
    F{Entity Disambiguation & Merging} \--\> G;  
    G \--\> H;

1. **Document Ingestion and Chunking:** The pipeline begins by ingesting documents from various sources. Since LLMs have a limited context window, large documents must be broken down into smaller, semantically coherent chunks.44 This can be done based on paragraphs, sections, or other logical divisions.  
2. **LLM-based Entity and Relationship Extraction:** Each text chunk is passed to an LLM with a carefully crafted prompt. This prompt instructs the model to act as an information extraction engine, identifying key entities (like features, requirements, or business concepts) and the relationships between them.46 To ensure a consistent and machine-readable output, the prompt specifies that the results should be formatted as a JSON object conforming to a predefined schema (e.g., a Pydantic model).48 This produces a list of potential nodes and relationships for each chunk.  
3. **Specialized Parsing for Embedded Diagrams:** Technical documents often contain diagrams, such as Mermaid sequence or class diagrams, which explicitly define system structure and interactions.49 The pipeline should include a step to detect these diagram blocks. If a Mermaid diagram is found, its text-based definition can be parsed programmatically to extract its entities and relationships directly.50 For example, an  
   erDiagram can be parsed to create :Feature nodes and DESCRIBES relationships with full fidelity.53 This structured data is often more reliable than LLM extraction and can supplement or validate the LLM's output.  
4. **Entity Disambiguation and Graph Loading:** Because entities are extracted from individual chunks, the same conceptual entity (e.g., "User Login Feature") may be extracted multiple times with slight variations. An entity disambiguation step is required to merge these duplicates.44 This can also be performed by an LLM, which is given groups of similar entities and asked to consolidate them into a single, canonical entity. Finally, the cleaned and consolidated list of nodes and relationships is converted into batched  
   MERGE statements and loaded into Neo4j, creating the :Document and :Feature nodes and linking them to the existing code graph.

## **III. Enabling Code Intelligence: Querying the Graph for LLM Context**

The value of the meticulously constructed code graph is realized through the queries executed against it. This section provides a "cookbook" of Cypher query patterns, progressing from foundational, LSP-like lookups to complex, multi-hop traversals designed to furnish an LLM with deep, actionable context. Cypher's declarative, ASCII-art-like syntax is particularly well-suited for expressing these graph patterns intuitively.38

### **3.1. Foundational Queries (LSP-like Features)**

These queries form the bedrock of code intelligence, replicating the core functionalities of the Language Server Protocol (LSP). They provide the LLM with the basic ability to navigate and understand the static structure of the code.

* **Go to Definition:** This is the most fundamental code navigation feature. Given a unique SCIP symbol for a usage of a variable or function, this query finds the node where that symbol was originally defined.  
  Cypher  
  // Purpose: Find the definition of a symbol.  
  // Parameter: $symbol \- The SCIP-formatted symbol string, e.g., "scip-python python my\_service 1.0 my\_module/my\_function()."  
  MATCH (s:Symbol {symbol: $symbol})\<--(definition)  
  RETURN  
      labels(definition) AS nodeType,  
      definition.name AS name,  
      definition.signature AS signature,  
      properties(definition) AS allProperties

  This query starts at the unique :Symbol node and traverses the incoming :DEFINES relationship to locate the concrete definition node (e.g., a :Function or :Class). It returns the node's type, key identifiers, and all its properties for the LLM to consume.  
* **Find All References:** This is the inverse of "go to definition." It finds every location in the codebase that uses a given symbol.  
  Cypher  
  // Purpose: Find all usage sites for a given symbol.  
  // Parameter: $symbol \- The SCIP-formatted symbol string, e.g., "scip-python python my\_service 1.0 my\_module/MyClass\#"  
  MATCH (s:Symbol {symbol: $symbol})\<--(usage)  
  // Traverse up the containment hierarchy to find the containing file  
  MATCH (usage)\<--(file:File)  
  RETURN  
      file.path AS filePath,  
      usage.startLine AS startLine,  
      usage.endLine AS endLine,  
      usage.name AS usageText

  This query finds all nodes that have a :REFERENCES relationship to the target :Symbol node. For each usage, it traverses up the :CONTAINS hierarchy to find the parent :File node, returning the location information needed to pinpoint the reference in the source code.  
* **Find Implementations of an Interface:** For object-oriented or interface-based programming, this query identifies all classes that implement a given interface.  
  Cypher  
  // Purpose: Find all classes that implement a specific interface.  
  // Parameter: $interfaceSymbol \- The SCIP symbol for the interface definition.  
  MATCH (interfaceSymbol:Symbol {symbol: $interfaceSymbol})  
  MATCH (interfaceSymbol)\<--(interfaceNode:Interface)  
  MATCH (interfaceNode)\<--(classNode:Class)  
  RETURN  
      classNode.name AS className,  
      classNode.fqn AS fullyQualifiedName,  
      classNode.filePath AS filePath

  This query demonstrates a multi-step traversal: from the interface's :Symbol to its :Interface definition node, and then across the :IMPLEMENTS relationships to find all implementing :Class nodes.

### **3.2. Advanced Contextual Queries for LLMs**

These queries unlock the true potential of the CPG-inspired model. They perform complex traversals that answer questions about program behavior, dependencies, and the impact of changes—information that is critical for an LLM to perform high-level coding tasks like refactoring, debugging, and feature implementation. These queries often use variable-length path traversals (-\[r\*..\]-\>) to explore the graph to an arbitrary depth.40

* Impact Analysis: "What API endpoints are affected by changing this function?"  
  This query automates a critical developer task: impact analysis. It traverses the call graph downstream from a specific function to identify all public-facing API endpoints that could be affected by a change. This allows the LLM to assess the blast radius of a proposed modification.  
  Cypher  
  // Purpose: Identify all API endpoints transitively dependent on a given function.  
  // Parameter: $startFunctionSymbol \- The SCIP symbol of the function being changed.  
  MATCH (startFunc)--\>(:Symbol {symbol: $startFunctionSymbol})  
  WHERE startFunc:Function OR startFunc:Method

  // Find all functions and methods called by startFunc, up to 10 levels deep.  
  // The direction of the CALLS relationship is from caller to callee.  
  MATCH (startFunc)--\>(downstream)  
  WHERE downstream:Function OR downstream:Method

  // From the set of downstream functions, find any that directly handle an API route.  
  MATCH (downstream)--\>(route:APIRoute)

  RETURN DISTINCT  
      route.protocol AS protocol,  
      route.method AS httpMethod,  
      route.path AS apiPath

  This traversal acts as a form of automated, structured code review. It instantly performs a trace that would be tedious and error-prone for a human, providing the LLM with a complete impact assessment before it generates or modifies code.  
* Data Lineage: "Trace this parameter to every function call where its value is used."  
  This query leverages the :FLOWS\_TO relationships to perform data lineage or taint tracking. It can show how data from a specific source (like an API request parameter) propagates through the system, which is invaluable for debugging, security analysis, and understanding business logic.  
  Cypher  
  // Purpose: Trace the flow of data from a specific parameter to its usage in other function calls.  
  // Parameter: $paramSymbol \- The SCIP symbol of the source parameter.  
  MATCH (param:Parameter)--\>(:Symbol {symbol: $paramSymbol})

  // Follow the data flow path through intermediate variables. The path can be up to 15 steps long.  
  MATCH path \= (param)--\>(usage)

  // Identify if the final usage is a parameter in another function call.  
  MATCH (usage:Parameter)\<--(call:Method)  
  WHERE usage:Parameter

  RETURN  
      call.name AS receivingMethod,  
      call.signature AS receivingMethodSignature,  
      nodes(path) AS dataFlowPath

  By making data flow a first-class relationship in the graph, we enable the LLM to reason about how information moves through the application, a much deeper level of understanding than static call graphs alone can provide.  
* Dependency Discovery: "What external services does this service call, and via which functions?"  
  This query helps map the explicit network dependencies between microservices. It identifies all calls that cross a service boundary by inspecting the SCIP symbols of the callees.  
  Cypher  
  // Purpose: Discover all external microservice dependencies originating from a given service.  
  // Parameter: $serviceName \- The name of the source service, e.g., "order-service".  
  MATCH (s:Service {name: $serviceName})

  // Find all functions/methods defined within this service.  
  MATCH (s)--\>(caller)  
  WHERE caller:Function OR caller:Method

  // Find all calls originating from these functions to symbols in other services.  
  MATCH (caller)--\>(symbol:Symbol)  
  // SCIP symbols for external packages contain the package name, which we use to identify the foreign service.  
  // We filter out internal calls by checking that the symbol does not contain the current service's name.  
  WHERE symbol.symbol CONTAINS " " AND NOT symbol.symbol CONTAINS $serviceName

  // Extract the foreign service name from the SCIP symbol string.  
  WITH caller, split(symbol.symbol, ' ') AS foreignServiceName, symbol  
  RETURN DISTINCT  
      foreignServiceName,  
      caller.name AS callingFunction,  
      symbol.symbol AS targetSymbol  
  ORDER BY foreignServiceName, callingFunction

  This query transforms the implicit knowledge buried in source code and dependency files into an explicit, queryable map of the microservice architecture's communication patterns. This holistic view is essential for an LLM tasked with tasks that span multiple services. The graph becomes more than just a code index; it evolves into an extensible knowledge base that can be enriched with other data sources, such as runtime observability metrics or security scan results, to provide an even richer context for AI-driven development.

### **3.3. Bridging Code and Context: Queries Across Documents and Source**

By integrating documents into the graph, we unlock a new class of queries that connect business intent with technical implementation. These queries allow the LLM to answer questions that require understanding both the "why" (from documents) and the "how" (from code).

* Feature to Code Traceability: "Find all functions that implement the 'Single Sign-On' feature."  
  This query allows the LLM to start from a high-level feature name and discover all the concrete code artifacts that contribute to its implementation.  
  Cypher  
  // Purpose: Find all code elements that implement a named feature.  
  // Parameter: $featureName \- The name of the feature, e.g., "Single Sign-On".  
  MATCH (feature:Feature {name: $featureName})  
  MATCH (feature)\<--(code)  
  WHERE code:Function OR code:Method OR code:Class  
  MATCH (code)\<--(file:File)  
  RETURN  
    feature.name AS feature,  
    labels(code) AS codeType,  
    code.name AS codeName,  
    file.path AS filePath

  This query starts at the :Feature node and traverses the incoming :IMPLEMENTS relationships to find all connected code elements, providing a complete manifest of the feature's implementation.  
* Code to Requirement Traceability: "What business requirement does this API endpoint fulfill?"  
  This is the inverse query, allowing the LLM to understand the business motivation behind a specific piece of code. This is invaluable for tasks like assessing the impact of a deprecation or explaining the purpose of a complex function.  
  Cypher  
  // Purpose: Find the business/technical requirements associated with a specific API endpoint.  
  // Parameter: $apiPath \- The path of the API endpoint, e.g., "/api/auth/sso/callback".  
  MATCH (route:APIRoute {path: $apiPath})  
  MATCH (route)\<--(handler)  
  MATCH (handler)--\>(feature:Feature)  
  MATCH (feature)\<--(doc:Document)  
  RETURN  
    route.path AS apiEndpoint,  
    handler.name AS handlerFunction,  
    feature.name AS feature,  
    feature.description AS featureDescription,  
    doc.title AS sourceDocument,  
    doc.type AS documentType

  This query traverses from an :APIRoute to its handler function, then across the :IMPLEMENTS relationship to the :Feature it realizes, and finally up to the :Document that DESCRIBES the feature, providing a full chain of context from code to requirement.

## **IV. Conclusions and Recommendations**

The architecture detailed in this report provides a robust and scalable foundation for building a next-generation code intelligence platform. By integrating tree-sitter, SCIP, and a Code Property Graph model within Neo4j, the system is designed to furnish a Large Language Model with the deep, multi-faceted context required for complex software engineering tasks across a microservices landscape.

The analysis yields several key conclusions that should guide the implementation:

1. **A CPG-Inspired Graph Model is Non-Negotiable for Deep Context:** The primary objective to move beyond simple text search can only be achieved by adopting a data model that explicitly represents control flow (:NEXT\_EXECUTION relationships) and data dependencies (:FLOWS\_TO relationships). A pure AST representation, while simpler to implement, would fundamentally limit the system's analytical capabilities and fail to provide the semantic context an LLM needs to reason about program behavior and impact.  
2. **A Multi-Pipeline Architecture is Essential:** The distinct operational requirements of indexing static code, dynamic code, and unstructured documents necessitate three specialized ingestion pipelines. The static pipeline should leverage compiler-backed SCIP indexers for accuracy, the dynamic pipeline must use tree-sitter for low-latency updates, and the document pipeline should employ LLMs to extract structured knowledge from unstructured text.  
3. **Standardized Symbols are the Key to Unifying the Graph:** The adoption of SCIP's standardized symbol format is the linchpin for enabling cross-service analysis. Using these globally unique strings as the primary key for :Symbol nodes provides a simple yet powerful mechanism to stitch together the individual graphs of each microservice, allowing queries to seamlessly traverse across repository and service boundaries.  
4. **Documents and APIs Must Be First-Class Citizens:** To create a truly comprehensive knowledge graph, both network interactions and the documents that specify system behavior must be modeled explicitly. Representing :APIRoute nodes and :Document nodes allows the graph to capture the full spectrum of context, from high-level business requirements to low-level implementation details.

**Actionable Recommendations for Implementation:**

* **Prioritize Schema Implementation:** The initial development effort should focus on establishing the core Neo4j schema as defined in Section 1.2. This provides the foundational structure for all subsequent work.  
* **Develop the Static Indexing Pipeline First:** This pipeline is more straightforward to implement as it relies on existing SCIP indexer tools and well-defined batch loading patterns in Neo4j. Successfully implementing this pipeline will provide immediate value by creating a comprehensive, queryable map of the stable services in the ecosystem.  
* **Prototype the Document Ingestion Pipeline:** Concurrently, begin prototyping the LLM-based document pipeline. Start with a single document type (e.g., PRDs in Markdown) to refine the information extraction prompts and the entity disambiguation logic. This will provide crucial business-level context early in the project.  
* **Focus Custom Engineering on the "AST-to-Cypher Translator":** The component responsible for translating tree-sitter AST diffs into incremental Cypher queries is the most significant custom engineering challenge in this architecture. This module requires careful design to correctly handle all types of code edits (additions, deletions, modifications) and their impact on graph relationships. Prototyping this for a single language is a recommended starting point.  
* **Build a Library of Contextual Queries:** Concurrently with backend development, the team should begin building and testing the advanced Cypher queries outlined in Section 3\. These queries represent the "API" that the LLM will use to interact with the code graph. Defining and refining these early will ensure the graph model and indexing pipelines are built to support the most valuable use cases.

By following this blueprint, it is possible to construct a powerful and unique code intelligence platform that not only provides LSP-like features but also enables a new class of system-level reasoning, transforming the LLM from a simple code completion tool into a true AI partner in the software development lifecycle.

#### **Works cited**

1. Abstract syntax tree \- Wikipedia, accessed August 29, 2025, [https://en.wikipedia.org/wiki/Abstract\_syntax\_tree](https://en.wikipedia.org/wiki/Abstract_syntax_tree)  
2. Use of graph databases for static code analysis, accessed August 29, 2025, [https://richardg.users.greyc.fr/publis/Dauprat-All\_2022.pdf](https://richardg.users.greyc.fr/publis/Dauprat-All_2022.pdf)  
3. Expressive and Scalable Source Code Queries with Graph Databases \- ELTE, accessed August 29, 2025, [https://people.inf.elte.hu/kiss/14kor/oopsla13.pdf](https://people.inf.elte.hu/kiss/14kor/oopsla13.pdf)  
4. Code Property Graph | Qwiet Docs, accessed August 29, 2025, [https://docs.shiftleft.io/core-concepts/code-property-graph](https://docs.shiftleft.io/core-concepts/code-property-graph)  
5. Modeling and Discovering Vulnerabilities with Code Property Graphs, accessed August 29, 2025, [https://www.ieee-security.org/TC/SP2014/papers/ModelingandDiscoveringVulnerabilitieswithCodePropertyGraphs.pdf](https://www.ieee-security.org/TC/SP2014/papers/ModelingandDiscoveringVulnerabilitieswithCodePropertyGraphs.pdf)  
6. Code Property Graph based Vulnerability Type Identification with Fusion Representation | Request PDF \- ResearchGate, accessed August 29, 2025, [https://www.researchgate.net/publication/371827192\_Code\_Property\_Graph\_based\_Vulnerability\_Type\_Identification\_with\_Fusion\_Representation](https://www.researchgate.net/publication/371827192_Code_Property_Graph_based_Vulnerability_Type_Identification_with_Fusion_Representation)  
7. Research on API Sequence Recommendation Based on Code Property Graph, accessed August 29, 2025, [https://www.computer.org/csdl/proceedings-article/iccbd+ai/2024/339900a306/25oLQKnIego](https://www.computer.org/csdl/proceedings-article/iccbd+ai/2024/339900a306/25oLQKnIego)  
8. CPGVA: Code Property Graph based Vulnerability Analysis by Deep Learning | Request PDF \- ResearchGate, accessed August 29, 2025, [https://www.researchgate.net/publication/332374604\_CPGVA\_Code\_Property\_Graph\_based\_Vulnerability\_Analysis\_by\_Deep\_Learning](https://www.researchgate.net/publication/332374604_CPGVA_Code_Property_Graph_based_Vulnerability_Analysis_by_Deep_Learning)  
9. Storing system information in a graph database? \- Elixir Forum, accessed August 29, 2025, [https://elixirforum.com/t/storing-system-information-in-a-graph-database/54159](https://elixirforum.com/t/storing-system-information-in-a-graph-database/54159)  
10. What is a graph database \- Getting Started \- Neo4j, accessed August 29, 2025, [https://neo4j.com/docs/getting-started/graph-database/](https://neo4j.com/docs/getting-started/graph-database/)  
11. SCIP \- a better code indexing format than LSIF | Sourcegraph Blog, accessed August 29, 2025, [https://sourcegraph.com/blog/announcing-scip](https://sourcegraph.com/blog/announcing-scip)  
12. sourcegraph/scip: SCIP Code Intelligence Protocol \- GitHub, accessed August 29, 2025, [https://github.com/sourcegraph/scip](https://github.com/sourcegraph/scip)  
13. scip-python: a precise Python indexer | Sourcegraph Blog, accessed August 29, 2025, [https://sourcegraph.com/blog/scip-python](https://sourcegraph.com/blog/scip-python)  
14. FAQ \- OpenAPI Initiative, accessed August 29, 2025, [https://www.openapis.org/faq](https://www.openapis.org/faq)  
15. Generates a graph visualisation from Paths in Swagger API definitions. \- GitHub, accessed August 29, 2025, [https://github.com/mcf-rocha/swagger-graph](https://github.com/mcf-rocha/swagger-graph)  
16. Using the gRPC API \- Software Heritage documentation, accessed August 29, 2025, [https://docs.softwareheritage.org/devel/swh-graph/grpc-api.html](https://docs.softwareheritage.org/devel/swh-graph/grpc-api.html)  
17. Visualize gRPC and Protocol Buffers \- Stack Overflow, accessed August 29, 2025, [https://stackoverflow.com/questions/40657881/visualize-grpc-and-protocol-buffers](https://stackoverflow.com/questions/40657881/visualize-grpc-and-protocol-buffers)  
18. scip-java · Java indexer for the SCIP Code Intelligence Protocol, accessed August 29, 2025, [https://sourcegraph.github.io/scip-java/](https://sourcegraph.github.io/scip-java/)  
19. Getting started · scip-java \- sourcegraph.github.io, accessed August 29, 2025, [https://sourcegraph.github.io/scip-java/docs/getting-started.html](https://sourcegraph.github.io/scip-java/docs/getting-started.html)  
20. sourcegraph/scip-python: SCIP indexer for Python \- GitHub, accessed August 29, 2025, [https://github.com/sourcegraph/scip-python](https://github.com/sourcegraph/scip-python)  
21. scip-typescript: a new TypeScript and JavaScript indexer | Sourcegraph Blog, accessed August 29, 2025, [https://sourcegraph.com/blog/announcing-scip-typescript](https://sourcegraph.com/blog/announcing-scip-typescript)  
22. README.md \- . \- sourcegraph/scip-clang, accessed August 29, 2025, [https://docs.sourcegraph.com/code\_intelligence/how-to/index\_a\_cpp\_repository](https://docs.sourcegraph.com/code_intelligence/how-to/index_a_cpp_repository)  
23. Decoding SCIP index file \- Sourcegraph Help Center, accessed August 29, 2025, [https://help.sourcegraph.com/hc/en-us/articles/15045932124941-Decoding-SCIP-index-file](https://help.sourcegraph.com/hc/en-us/articles/15045932124941-Decoding-SCIP-index-file)  
24. Import: RDBMS to graph \- Getting Started \- Neo4j, accessed August 29, 2025, [https://neo4j.com/docs/getting-started/data-import/relational-to-graph-import/](https://neo4j.com/docs/getting-started/data-import/relational-to-graph-import/)  
25. Tutorial: Import data from a relational database into Neo4j \- Getting Started, accessed August 29, 2025, [https://neo4j.com/docs/getting-started/appendix/tutorials/guide-import-relational-and-etl/](https://neo4j.com/docs/getting-started/appendix/tutorials/guide-import-relational-and-etl/)  
26. 5 Tips & Tricks for Fast Batched Updates of Graph Structures with Neo4j and Cypher, accessed August 29, 2025, [https://medium.com/neo4j/5-tips-tricks-for-fast-batched-updates-of-graph-structures-with-neo4j-and-cypher-73c7f693c8cc](https://medium.com/neo4j/5-tips-tricks-for-fast-batched-updates-of-graph-structures-with-neo4j-and-cypher-73c7f693c8cc)  
27. Incremental Parsing Using Tree-sitter \- Strumenta \- Federico Tomassetti, accessed August 29, 2025, [https://tomassetti.me/incremental-parsing-using-tree-sitter/](https://tomassetti.me/incremental-parsing-using-tree-sitter/)  
28. tree-sitter/tree-sitter: An incremental parsing system for programming tools \- GitHub, accessed August 29, 2025, [https://github.com/tree-sitter/tree-sitter](https://github.com/tree-sitter/tree-sitter)  
29. Tree — py-tree-sitter 0.25.1 documentation, accessed August 29, 2025, [https://tree-sitter.github.io/py-tree-sitter/classes/tree\_sitter.Tree.html](https://tree-sitter.github.io/py-tree-sitter/classes/tree_sitter.Tree.html)  
30. Diving into Tree-Sitter: Parsing Code with Python Like a Pro \- DEV Community, accessed August 29, 2025, [https://dev.to/shrsv/diving-into-tree-sitter-parsing-code-with-python-like-a-pro-17h8](https://dev.to/shrsv/diving-into-tree-sitter-parsing-code-with-python-like-a-pro-17h8)  
31. tree-sitter \- GitHub, accessed August 29, 2025, [https://github.com/tree-sitter](https://github.com/tree-sitter)  
32. Tree-sitter: an incremental parsing system for programming tools | Hacker News, accessed August 29, 2025, [https://news.ycombinator.com/item?id=26225298](https://news.ycombinator.com/item?id=26225298)  
33. Dynamic module binding for tree-sitter, an incremental parsing library : r/emacs \- Reddit, accessed August 29, 2025, [https://www.reddit.com/r/emacs/comments/chnxzm/dynamic\_module\_binding\_for\_treesitter\_an/](https://www.reddit.com/r/emacs/comments/chnxzm/dynamic_module_binding_for_treesitter_an/)  
34. Diffsitter – A Tree-sitter based AST difftool to get meaningful semantic diffs | Hacker News, accessed August 29, 2025, [https://news.ycombinator.com/item?id=44520438](https://news.ycombinator.com/item?id=44520438)  
35. afnanenayet/diffsitter: A tree-sitter based AST difftool to get meaningful semantic diffs \- GitHub, accessed August 29, 2025, [https://github.com/afnanenayet/diffsitter](https://github.com/afnanenayet/diffsitter)  
36. Tree Diffing \- Difftastic Manual, accessed August 29, 2025, [https://difftastic.wilfred.me.uk/tree\_diffing.html](https://difftastic.wilfred.me.uk/tree_diffing.html)  
37. Updating the data \- Getting Started \- Neo4j, accessed August 29, 2025, [https://neo4j.com/docs/getting-started/cypher-intro/updating/](https://neo4j.com/docs/getting-started/cypher-intro/updating/)  
38. What is Cypher \- Getting Started \- Neo4j, accessed August 29, 2025, [https://neo4j.com/docs/getting-started/cypher/](https://neo4j.com/docs/getting-started/cypher/)  
39. Cypher (query language) \- Wikipedia, accessed August 29, 2025, [https://en.wikipedia.org/wiki/Cypher\_(query\_language)](https://en.wikipedia.org/wiki/Cypher_\(query_language\))  
40. Basic queries \- Cypher Manual \- Neo4j, accessed August 29, 2025, [https://neo4j.com/docs/cypher-manual/current/queries/basic/](https://neo4j.com/docs/cypher-manual/current/queries/basic/)  
41. Cypher Cheat Sheet \- Neo4j, accessed August 29, 2025, [https://neo4j.com/docs/cypher-cheat-sheet/current/](https://neo4j.com/docs/cypher-cheat-sheet/current/)  
42. Your Technical Documentation Should Be a Graph: Here's Why, accessed September 13, 2025, [https://neo4j.com/blog/knowledge-graph/technical-documentation-graph/](https://neo4j.com/blog/knowledge-graph/technical-documentation-graph/)  
43. Graph Database Use Cases \- Match Your Projects with Skills, accessed September 13, 2025, [https://www.synergycodes.com/graph-database-case-study](https://www.synergycodes.com/graph-database-case-study)  
44. How to Convert Unstructured Text to Knowledge Graphs Using LLMs, accessed September 13, 2025, [https://neo4j.com/blog/developer/unstructured-text-to-knowledge-graph/](https://neo4j.com/blog/developer/unstructured-text-to-knowledge-graph/)  
45. Information extraction with LLMs using Amazon SageMaker JumpStart | Artificial Intelligence, accessed September 13, 2025, [https://aws.amazon.com/blogs/machine-learning/information-extraction-with-llms-using-amazon-sagemaker-jumpstart/](https://aws.amazon.com/blogs/machine-learning/information-extraction-with-llms-using-amazon-sagemaker-jumpstart/)  
46. Using LLM to Extract Knowledge Graph Entities and Relationships \- TiDB, accessed September 13, 2025, [https://www.pingcap.com/article/using-llm-extract-knowledge-graph-entities-and-relationships/](https://www.pingcap.com/article/using-llm-extract-knowledge-graph-entities-and-relationships/)  
47. Extract Information from Hybrid Long Documents Leveraging LLMs: A Framework and Dataset \- arXiv, accessed September 13, 2025, [https://arxiv.org/html/2412.20072](https://arxiv.org/html/2412.20072)  
48. Building Knowledge Graphs Using Large Language Models | by ..., accessed September 13, 2025, [https://medium.com/@shuchawl/building-knowledge-graphs-using-large-language-models-07da1935b21a](https://medium.com/@shuchawl/building-knowledge-graphs-using-large-language-models-07da1935b21a)  
49. Documentation as Code and Diagram as Code Tools of 2023 \- workingsoftware.dev, accessed September 13, 2025, [https://www.workingsoftware.dev/documentation-as-code-tools/](https://www.workingsoftware.dev/documentation-as-code-tools/)  
50. mermaid-js/mermaid: Generation of diagrams like flowcharts or sequence diagrams from text in a similar manner as markdown \- GitHub, accessed September 13, 2025, [https://github.com/mermaid-js/mermaid](https://github.com/mermaid-js/mermaid)  
51. Is there anyone with better idea for parsing Mermaid sequence diagrams : r/golang \- Reddit, accessed September 13, 2025, [https://www.reddit.com/r/golang/comments/1lx1mtk/is\_there\_anyone\_with\_better\_idea\_for\_parsing/](https://www.reddit.com/r/golang/comments/1lx1mtk/is_there_anyone_with_better_idea_for_parsing/)  
52. mermaid-js/parser \- NPM, accessed September 13, 2025, [https://www.npmjs.com/package/@mermaid-js/parser](https://www.npmjs.com/package/@mermaid-js/parser)  
53. Entity Relationship Diagram \- Mermaid Chart, accessed September 13, 2025, [https://docs.mermaidchart.com/mermaid-oss/syntax/entityRelationshipDiagram.html](https://docs.mermaidchart.com/mermaid-oss/syntax/entityRelationshipDiagram.html)
