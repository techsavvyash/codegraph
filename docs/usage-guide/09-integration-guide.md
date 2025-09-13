# Integration Guide

This guide covers integrating CodeGraph with LLMs, IDEs, CI/CD pipelines, and other development tools for maximum productivity.

## LLM Integration

CodeGraph's precise source code retrieval makes it ideal for LLM-powered code analysis and generation.

### Basic LLM Integration Pattern

```go
package main

import (
    "context"
    "fmt"
    "strings"
    
    "github.com/context-maximiser/code-graph/pkg/neo4j"
)

// LLMCodeAnalyzer integrates CodeGraph with LLMs
type LLMCodeAnalyzer struct {
    queryBuilder *neo4j.QueryBuilder
    llmClient    LLMClient // Your LLM client (OpenAI, Anthropic, etc.)
}

// AnalyzeFunction uses LLM to analyze a specific function
func (analyzer *LLMCodeAnalyzer) AnalyzeFunction(ctx context.Context, functionName string) (*Analysis, error) {
    // Step 1: Retrieve precise source code from CodeGraph
    sourceCode, err := analyzer.queryBuilder.GetFunctionSourceCode(ctx, functionName)
    if err != nil {
        return nil, fmt.Errorf("failed to get source code: %w", err)
    }
    
    // Step 2: Get related context (callers, callees, etc.)
    relatedFunctions, err := analyzer.getRelatedFunctions(ctx, functionName)
    if err != nil {
        // Continue without related functions
        relatedFunctions = []string{}
    }
    
    // Step 3: Prepare LLM prompt
    prompt := analyzer.buildAnalysisPrompt(sourceCode, relatedFunctions)
    
    // Step 4: Call LLM
    response, err := analyzer.llmClient.Complete(ctx, prompt)
    if err != nil {
        return nil, fmt.Errorf("LLM analysis failed: %w", err)
    }
    
    // Step 5: Parse and return structured analysis
    return analyzer.parseAnalysis(response), nil
}

func (analyzer *LLMCodeAnalyzer) buildAnalysisPrompt(sourceCode string, relatedFuncs []string) string {
    var prompt strings.Builder
    
    prompt.WriteString("Analyze this Go function:\n\n")
    prompt.WriteString("```go\n")
    prompt.WriteString(sourceCode)
    prompt.WriteString("\n```\n\n")
    
    if len(relatedFuncs) > 0 {
        prompt.WriteString("Related functions in the codebase:\n")
        for _, fn := range relatedFuncs {
            prompt.WriteString(fmt.Sprintf("- %s\n", fn))
        }
        prompt.WriteString("\n")
    }
    
    prompt.WriteString(`Please provide:
1. **Purpose**: What does this function do?
2. **Complexity**: Cyclomatic complexity assessment
3. **Issues**: Potential bugs, code smells, or improvements
4. **Dependencies**: How it relates to other functions
5. **Test Suggestions**: What should be tested
6. **Documentation**: Suggested documentation improvements

Format your response as JSON:
{
  "purpose": "...",
  "complexity": "low|medium|high",
  "issues": ["issue1", "issue2"],
  "dependencies": ["func1", "func2"],
  "testSuggestions": ["test1", "test2"],
  "documentation": "..."
}`)

    return prompt.String()
}
```

### Advanced LLM Use Cases

#### 1. Code Review Assistant

```go
// CodeReviewAssistant provides LLM-powered code review
type CodeReviewAssistant struct {
    codeGraph *neo4j.QueryBuilder
    llm       LLMClient
}

func (cra *CodeReviewAssistant) ReviewChangedFunctions(ctx context.Context, changedFiles []string) (*ReviewReport, error) {
    var reviews []FunctionReview
    
    for _, filePath := range changedFiles {
        // Find functions in changed files
        functions, err := cra.findFunctionsInFile(ctx, filePath)
        if err != nil {
            continue
        }
        
        for _, functionName := range functions {
            // Get source code with precise location
            sourceCode, err := cra.codeGraph.GetFunctionSourceCode(ctx, functionName)
            if err != nil {
                continue
            }
            
            // Analyze with LLM
            review, err := cra.reviewFunction(ctx, functionName, sourceCode)
            if err != nil {
                continue
            }
            
            reviews = append(reviews, *review)
        }
    }
    
    return &ReviewReport{
        Reviews:   reviews,
        Summary:   cra.generateSummary(reviews),
        Timestamp: time.Now(),
    }, nil
}

func (cra *CodeReviewAssistant) reviewFunction(ctx context.Context, name, sourceCode string) (*FunctionReview, error) {
    prompt := fmt.Sprintf(`
Review this Go function for:
- Code quality and best practices
- Potential bugs or edge cases  
- Performance implications
- Security considerations
- Maintainability issues

Function: %s
Source Code:
%s

Provide a structured review with severity levels (info, warning, error).
`, name, sourceCode)

    response, err := cra.llm.Complete(ctx, prompt)
    if err != nil {
        return nil, err
    }
    
    return parseReviewResponse(response), nil
}
```

#### 2. Documentation Generator

```go
// DocumentationGenerator creates comprehensive docs using LLM + CodeGraph
type DocumentationGenerator struct {
    codeGraph *neo4j.QueryBuilder
    llm       LLMClient
}

func (dg *DocumentationGenerator) GeneratePackageDoc(ctx context.Context, packagePath string) (*PackageDocumentation, error) {
    // Get all functions in package
    functions, err := dg.findPackageFunctions(ctx, packagePath)
    if err != nil {
        return nil, err
    }
    
    var functionDocs []FunctionDoc
    for _, fn := range functions {
        // Get precise source code
        sourceCode, err := dg.codeGraph.GetFunctionSourceCode(ctx, fn.Name)
        if err != nil {
            continue
        }
        
        // Generate documentation with LLM
        doc, err := dg.generateFunctionDoc(ctx, fn.Name, sourceCode, fn.Signature)
        if err != nil {
            continue
        }
        
        functionDocs = append(functionDocs, *doc)
    }
    
    // Generate package overview
    overview, err := dg.generatePackageOverview(ctx, packagePath, functionDocs)
    if err != nil {
        return nil, err
    }
    
    return &PackageDocumentation{
        PackagePath:   packagePath,
        Overview:      overview,
        Functions:     functionDocs,
        GeneratedAt:   time.Now(),
    }, nil
}
```

#### 3. Code Refactoring Assistant  

```go
// RefactoringAssistant suggests and applies refactorings
type RefactoringAssistant struct {
    codeGraph *neo4j.QueryBuilder
    llm       LLMClient
}

func (ra *RefactoringAssistant) SuggestRefactoring(ctx context.Context, functionName string) (*RefactoringSuggestion, error) {
    // Get current implementation
    sourceCode, err := ra.codeGraph.GetFunctionSourceCode(ctx, functionName)
    if err != nil {
        return nil, err
    }
    
    // Get usage patterns (where it's called from)
    callers, err := ra.findFunctionCallers(ctx, functionName)
    if err != nil {
        callers = []string{} // Continue without callers
    }
    
    // Get functions it calls
    callees, err := ra.findFunctionCallees(ctx, functionName)
    if err != nil {
        callees = []string{} // Continue without callees  
    }
    
    prompt := fmt.Sprintf(`
Analyze this Go function and suggest refactoring improvements:

Function: %s
Current Implementation:
%s

Usage Context:
- Called by: %v
- Calls: %v

Suggest refactoring improvements for:
1. Readability and maintainability
2. Performance optimizations
3. Error handling improvements
4. Code structure and organization
5. Testing improvements

Provide the refactored code and explanation of changes.
`, functionName, sourceCode, callers, callees)

    response, err := ra.llm.Complete(ctx, prompt)
    if err != nil {
        return nil, err
    }
    
    return ra.parseRefactoringSuggestion(response), nil
}
```

## IDE Integration

### VS Code Extension

Create a VS Code extension that leverages CodeGraph:

```typescript
// src/extension.ts
import * as vscode from 'vscode';
import { CodeGraphClient } from './codegraph-client';

export function activate(context: vscode.ExtensionContext) {
    const client = new CodeGraphClient();
    
    // Command: Get function source code
    const getFunctionSourceCommand = vscode.commands.registerCommand(
        'codegraph.getFunctionSource',
        async () => {
            const editor = vscode.window.activeTextEditor;
            if (!editor) return;
            
            const document = editor.document;
            const position = editor.selection.active;
            const wordRange = document.getWordRangeAtPosition(position);
            const functionName = document.getText(wordRange);
            
            try {
                const sourceCode = await client.getFunctionSource(functionName);
                
                // Show in new editor
                const doc = await vscode.workspace.openTextDocument({
                    content: sourceCode,
                    language: 'go'
                });
                await vscode.window.showTextDocument(doc);
                
            } catch (error) {
                vscode.window.showErrorMessage(`Failed to get source: ${error}`);
            }
        }
    );
    
    // Command: Find function references
    const findReferencesCommand = vscode.commands.registerCommand(
        'codegraph.findReferences',
        async () => {
            const editor = vscode.window.activeTextEditor;
            if (!editor) return;
            
            const functionName = getCurrentFunctionName(editor);
            if (!functionName) return;
            
            try {
                const references = await client.findReferences(functionName);
                showReferencesPanel(references);
                
            } catch (error) {
                vscode.window.showErrorMessage(`Failed to find references: ${error}`);
            }
        }
    );
    
    // Hover provider for function information
    const hoverProvider = vscode.languages.registerHoverProvider('go', {
        async provideHover(document, position) {
            const wordRange = document.getWordRangeAtPosition(position);
            const word = document.getText(wordRange);
            
            try {
                const info = await client.getFunctionInfo(word);
                const markdown = new vscode.MarkdownString();
                markdown.appendCodeblock(info.signature, 'go');
                markdown.appendText(info.documentation);
                
                return new vscode.Hover(markdown);
                
            } catch (error) {
                return null; // No hover information available
            }
        }
    });
    
    context.subscriptions.push(
        getFunctionSourceCommand,
        findReferencesCommand,
        hoverProvider
    );
}

// CodeGraph client implementation
class CodeGraphClient {
    private baseUrl: string;
    
    constructor() {
        this.baseUrl = vscode.workspace.getConfiguration('codegraph').get('serverUrl') || 'http://localhost:8080';
    }
    
    async getFunctionSource(functionName: string): Promise<string> {
        const response = await fetch(`${this.baseUrl}/api/functions/${functionName}/source`);
        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }
        return await response.text();
    }
    
    async findReferences(functionName: string): Promise<Reference[]> {
        const response = await fetch(`${this.baseUrl}/api/functions/${functionName}/references`);
        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }
        return await response.json();
    }
    
    async getFunctionInfo(functionName: string): Promise<FunctionInfo> {
        const response = await fetch(`${this.baseUrl}/api/functions/${functionName}/info`);
        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }
        return await response.json();
    }
}
```

### JetBrains Plugin

```kotlin
// CodeGraphPlugin.kt
class CodeGraphPlugin : com.intellij.openapi.project.DumbAwareAction() {
    
    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val editor = e.getData(CommonDataKeys.EDITOR) ?: return
        
        val functionName = getCurrentFunctionName(editor)
        if (functionName.isNullOrEmpty()) return
        
        // Get function source from CodeGraph
        ApplicationManager.getApplication().executeOnPooledThread {
            try {
                val sourceCode = CodeGraphClient.instance.getFunctionSource(functionName)
                
                ApplicationManager.getApplication().invokeLater {
                    showSourceCodeDialog(project, functionName, sourceCode)
                }
                
            } catch (e: Exception) {
                showErrorMessage(project, "Failed to retrieve source code: ${e.message}")
            }
        }
    }
    
    private fun showSourceCodeDialog(project: Project, functionName: String, sourceCode: String) {
        val dialog = SourceCodeDialog(project, functionName, sourceCode)
        dialog.show()
    }
}

object CodeGraphClient {
    val instance = CodeGraphClient()
    private val client = OkHttpClient()
    
    fun getFunctionSource(functionName: String): String {
        val request = Request.Builder()
            .url("http://localhost:8080/api/functions/$functionName/source")
            .build()
            
        client.newCall(request).execute().use { response ->
            if (!response.isSuccessful) {
                throw IOException("HTTP ${response.code}: ${response.message}")
            }
            return response.body?.string() ?: ""
        }
    }
}
```

## CI/CD Pipeline Integration

### GitHub Actions Integration

```yaml
# .github/workflows/codegraph-analysis.yml
name: CodeGraph Analysis
on:
  pull_request:
    branches: [main]
    paths: ['**.go']

jobs:
  codegraph-analysis:
    runs-on: ubuntu-latest
    
    services:
      neo4j:
        image: neo4j:5.23.0
        env:
          NEO4J_AUTH: neo4j/password123
          NEO4J_PLUGINS: '["apoc","apoc-extended"]'
        ports:
          - 7687:7687
          - 7474:7474
        options: >-
          --health-cmd "cypher-shell -u neo4j -p password123 'RETURN 1'"
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5
    
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0  # Full history for change analysis
          
      - uses: actions/setup-go@v4
        with:
          go-version: '1.21'
          
      - name: Install dependencies
        run: |
          go install github.com/sourcegraph/scip-go/cmd/scip-go@latest
          go mod download
          
      - name: Build CodeGraph
        run: go build -o codegraph cmd/codegraph/main.go
        
      - name: Initialize schema
        run: ./codegraph schema create
        
      - name: Index current codebase
        run: |
          ./codegraph index project . --service="${{ github.repository }}"
          ./codegraph index scip . --service="${{ github.repository }}-scip"
          
      - name: Analyze changed functions
        id: analysis
        run: |
          # Get changed Go files
          CHANGED_FILES=$(git diff --name-only origin/main...HEAD | grep '\.go$' || true)
          
          if [ -z "$CHANGED_FILES" ]; then
            echo "No Go files changed"
            exit 0
          fi
          
          # Analyze each changed file
          echo "## CodeGraph Analysis" >> analysis.md
          echo "" >> analysis.md
          
          for file in $CHANGED_FILES; do
            echo "### File: $file" >> analysis.md
            echo "" >> analysis.md
            
            # Find functions in file and analyze them
            FUNCTIONS=$(grep -n "^func " "$file" | cut -d: -f2 | awk '{print $2}' | cut -d'(' -f1 || true)
            
            for func in $FUNCTIONS; do
              echo "#### Function: $func" >> analysis.md
              echo "" >> analysis.md
              
              # Get function source
              if SOURCE=$(./codegraph query source "$func" 2>/dev/null); then
                echo "\`\`\`go" >> analysis.md
                echo "$SOURCE" >> analysis.md
                echo "\`\`\`" >> analysis.md
                echo "" >> analysis.md
              else
                echo "Could not retrieve source for $func" >> analysis.md
              fi
            done
          done
          
          # Set output for later steps
          echo "analysis-file=analysis.md" >> $GITHUB_OUTPUT
          
      - name: Comment PR with analysis
        uses: actions/github-script@v7
        with:
          script: |
            const fs = require('fs');
            const analysisFile = '${{ steps.analysis.outputs.analysis-file }}';
            
            if (fs.existsSync(analysisFile)) {
              const analysis = fs.readFileSync(analysisFile, 'utf8');
              
              // Create or update comment
              const comments = await github.rest.issues.listComments({
                owner: context.repo.owner,
                repo: context.repo.repo,
                issue_number: context.issue.number,
              });
              
              const botComment = comments.data.find(
                comment => comment.user.type === 'Bot' && 
                          comment.body.includes('CodeGraph Analysis')
              );
              
              if (botComment) {
                // Update existing comment
                await github.rest.issues.updateComment({
                  owner: context.repo.owner,
                  repo: context.repo.repo,
                  comment_id: botComment.id,
                  body: analysis
                });
              } else {
                // Create new comment
                await github.rest.issues.createComment({
                  owner: context.repo.owner,
                  repo: context.repo.repo,
                  issue_number: context.issue.number,
                  body: analysis
                });
              }
            }
```

### Jenkins Pipeline Integration

```groovy
// Jenkinsfile
pipeline {
    agent any
    
    environment {
        NEO4J_URI = 'bolt://neo4j-server:7687'
        NEO4J_USER = 'neo4j'
        NEO4J_PASSWORD = credentials('neo4j-password')
    }
    
    stages {
        stage('Checkout') {
            steps {
                checkout scm
            }
        }
        
        stage('Setup Dependencies') {
            steps {
                sh '''
                    go version
                    go install github.com/sourcegraph/scip-go/cmd/scip-go@latest
                    go mod download
                '''
            }
        }
        
        stage('Build CodeGraph') {
            steps {
                sh 'go build -o codegraph cmd/codegraph/main.go'
            }
        }
        
        stage('Initialize Schema') {
            steps {
                sh './codegraph schema create'
            }
        }
        
        stage('Index Codebase') {
            parallel {
                stage('AST Index') {
                    steps {
                        sh '''
                            ./codegraph index project . \\
                              --service="${JOB_NAME}" \\
                              --version="${BUILD_NUMBER}"
                        '''
                    }
                }
                stage('SCIP Index') {
                    steps {
                        sh '''
                            ./codegraph index scip . \\
                              --service="${JOB_NAME}-scip" \\
                              --version="${BUILD_NUMBER}"
                        '''
                    }
                }
            }
        }
        
        stage('Quality Analysis') {
            steps {
                sh '''
                    # Generate quality report
                    ./codegraph query search "*" --limit 100 > indexed-functions.txt
                    
                    # Count functions and generate metrics
                    TOTAL_FUNCTIONS=$(grep -r "^func " . --include="*.go" | wc -l)
                    INDEXED_FUNCTIONS=$(cat indexed-functions.txt | wc -l)
                    COVERAGE=$((INDEXED_FUNCTIONS * 100 / TOTAL_FUNCTIONS))
                    
                    echo "Indexing Coverage: $COVERAGE%" > quality-report.txt
                    echo "Total Functions: $TOTAL_FUNCTIONS" >> quality-report.txt
                    echo "Indexed Functions: $INDEXED_FUNCTIONS" >> quality-report.txt
                '''
                
                archiveArtifacts artifacts: 'quality-report.txt'
                publishHTML([
                    allowMissing: false,
                    alwaysLinkToLastBuild: true,
                    keepAll: true,
                    reportDir: '.',
                    reportFiles: 'quality-report.txt',
                    reportName: 'CodeGraph Quality Report'
                ])
            }
        }
        
        stage('Function Analysis') {
            when {
                changeRequest()
            }
            steps {
                script {
                    // Analyze changed functions
                    def changedFiles = sh(
                        script: "git diff --name-only origin/main...HEAD | grep '\\.go\$' || true",
                        returnStdout: true
                    ).trim().split('\n')
                    
                    def analysis = []
                    
                    changedFiles.each { file ->
                        if (file) {
                            def functions = sh(
                                script: "grep -n '^func ' ${file} | cut -d: -f2 | awk '{print \$2}' | cut -d'(' -f1 || true",
                                returnStdout: true
                            ).trim().split('\n')
                            
                            functions.each { func ->
                                if (func) {
                                    try {
                                        def source = sh(
                                            script: "./codegraph query source '${func}'",
                                            returnStdout: true
                                        )
                                        analysis << [file: file, function: func, source: source]
                                    } catch (Exception e) {
                                        echo "Could not retrieve source for ${func}: ${e.message}"
                                    }
                                }
                            }
                        }
                    }
                    
                    // Store analysis results
                    writeJSON file: 'function-analysis.json', json: analysis
                }
            }
        }
    }
    
    post {
        always {
            // Cleanup
            sh 'docker-compose down || true'
        }
        success {
            echo 'CodeGraph analysis completed successfully!'
        }
        failure {
            echo 'CodeGraph analysis failed!'
        }
    }
}
```

## API Server Integration

Create a REST API server for external integrations:

```go
// cmd/codegraph-server/main.go
package main

import (
    "encoding/json"
    "log"
    "net/http"
    
    "github.com/gorilla/mux"
    "github.com/context-maximiser/code-graph/pkg/neo4j"
)

type Server struct {
    queryBuilder *neo4j.QueryBuilder
}

func main() {
    // Initialize CodeGraph client
    client, err := neo4j.NewClient(neo4j.Config{
        URI:      "bolt://localhost:7687",
        Username: "neo4j",
        Password: "password123", 
        Database: "neo4j",
    })
    if err != nil {
        log.Fatal("Failed to create Neo4j client:", err)
    }
    
    server := &Server{
        queryBuilder: neo4j.NewQueryBuilder(client),
    }
    
    // Setup routes
    r := mux.NewRouter()
    api := r.PathPrefix("/api/v1").Subrouter()
    
    // Function endpoints
    api.HandleFunc("/functions/{name}/source", server.getFunctionSource).Methods("GET")
    api.HandleFunc("/functions/{name}/info", server.getFunctionInfo).Methods("GET")
    api.HandleFunc("/functions/{name}/references", server.getFunctionReferences).Methods("GET")
    api.HandleFunc("/functions/{name}/callers", server.getFunctionCallers).Methods("GET")
    api.HandleFunc("/functions/{name}/callees", server.getFunctionCallees).Methods("GET")
    
    // Search endpoints
    api.HandleFunc("/search", server.search).Methods("GET")
    api.HandleFunc("/search/functions", server.searchFunctions).Methods("GET")
    
    // Health endpoint
    api.HandleFunc("/health", server.health).Methods("GET")
    
    log.Println("Starting CodeGraph API server on :8080")
    log.Fatal(http.ListenAndServe(":8080", r))
}

func (s *Server) getFunctionSource(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    functionName := vars["name"]
    
    sourceCode, err := s.queryBuilder.GetFunctionSourceCode(r.Context(), functionName)
    if err != nil {
        http.Error(w, err.Error(), http.StatusNotFound)
        return
    }
    
    w.Header().Set("Content-Type", "text/plain")
    w.Write([]byte(sourceCode))
}

func (s *Server) getFunctionInfo(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    functionName := vars["name"]
    
    info, err := s.getFunctionMetadata(r.Context(), functionName)
    if err != nil {
        http.Error(w, err.Error(), http.StatusNotFound)
        return
    }
    
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(info)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
    query := r.URL.Query().Get("q")
    if query == "" {
        http.Error(w, "Missing query parameter 'q'", http.StatusBadRequest)
        return
    }
    
    limit := 20
    if limitParam := r.URL.Query().Get("limit"); limitParam != "" {
        fmt.Sscanf(limitParam, "%d", &limit)
    }
    
    results, err := s.queryBuilder.SearchNodes(r.Context(), query, nil, limit)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    
    // Convert results to API format
    apiResults := convertSearchResults(results)
    
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]interface{}{
        "query":   query,
        "results": apiResults,
        "count":   len(apiResults),
    })
}
```

## Webhook Integration

Set up webhooks for real-time integration with external systems:

```go
// Webhook handler for code changes
func handleCodeChangeWebhook(w http.ResponseWriter, r *http.Request) {
    var payload struct {
        Repository struct {
            Name     string `json:"name"`
            CloneURL string `json:"clone_url"`
        } `json:"repository"`
        Changes []struct {
            Filename string `json:"filename"`
            Status   string `json:"status"`
        } `json:"changes"`
    }
    
    if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
        http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
        return
    }
    
    // Filter Go files
    var changedGoFiles []string
    for _, change := range payload.Changes {
        if strings.HasSuffix(change.Filename, ".go") {
            changedGoFiles = append(changedGoFiles, change.Filename)
        }
    }
    
    if len(changedGoFiles) == 0 {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("No Go files changed"))
        return
    }
    
    // Queue incremental reindexing
    go func() {
        if err := reindexChangedFiles(payload.Repository.Name, changedGoFiles); err != nil {
            log.Printf("Failed to reindex changed files: %v", err)
        }
    }()
    
    w.WriteHeader(http.StatusAccepted)
    w.Write([]byte("Reindexing queued"))
}

func reindexChangedFiles(repoName string, files []string) error {
    // Implementation for incremental reindexing
    // This would analyze the changed files and update the graph accordingly
    log.Printf("Reindexing %d files for repository %s", len(files), repoName)
    // ... reindexing logic
    return nil
}
```

## Monitoring and Observability

### Prometheus Metrics Integration

```go
// metrics.go
package main

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

var (
    indexedFunctions = promauto.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "codegraph_indexed_functions_total",
            Help: "Total number of indexed functions",
        },
        []string{"service"},
    )
    
    queryDuration = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "codegraph_query_duration_seconds",
            Help: "Duration of CodeGraph queries",
        },
        []string{"query_type"},
    )
    
    sourceRetrievalDuration = promauto.NewHistogram(
        prometheus.HistogramOpts{
            Name: "codegraph_source_retrieval_duration_seconds",
            Help: "Duration of source code retrieval operations",
        },
    )
)

// Instrument your functions
func (qb *QueryBuilder) GetFunctionSourceCodeWithMetrics(ctx context.Context, functionName string) (string, error) {
    timer := prometheus.NewTimer(sourceRetrievalDuration)
    defer timer.ObserveDuration()
    
    return qb.GetFunctionSourceCode(ctx, functionName)
}
```

### Health Check Endpoint

```go
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
    health := struct {
        Status    string            `json:"status"`
        Timestamp string            `json:"timestamp"`
        Services  map[string]string `json:"services"`
    }{
        Status:    "healthy",
        Timestamp: time.Now().UTC().Format(time.RFC3339),
        Services:  make(map[string]string),
    }
    
    // Check Neo4j connectivity
    ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
    defer cancel()
    
    _, err := s.queryBuilder.SearchNodes(ctx, "test", nil, 1)
    if err != nil {
        health.Status = "unhealthy"
        health.Services["neo4j"] = "error: " + err.Error()
    } else {
        health.Services["neo4j"] = "healthy"
    }
    
    // Set HTTP status based on health
    if health.Status == "healthy" {
        w.WriteHeader(http.StatusOK)
    } else {
        w.WriteHeader(http.StatusServiceUnavailable)
    }
    
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(health)
}
```

## Security Considerations

### Authentication and Authorization

```go
// auth middleware
func authMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Extract API key from header
        apiKey := r.Header.Get("X-API-Key")
        if apiKey == "" {
            http.Error(w, "Missing API key", http.StatusUnauthorized)
            return
        }
        
        // Validate API key
        if !isValidAPIKey(apiKey) {
            http.Error(w, "Invalid API key", http.StatusUnauthorized)
            return
        }
        
        // Add user context
        ctx := context.WithValue(r.Context(), "user_id", getUserIDFromAPIKey(apiKey))
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}

// Apply to all API routes
api.Use(authMiddleware)
```

### Rate Limiting

```go
import "golang.org/x/time/rate"

func rateLimitMiddleware(rps rate.Limit, burst int) func(http.Handler) http.Handler {
    limiter := rate.NewLimiter(rps, burst)
    
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            if !limiter.Allow() {
                http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}

// Apply rate limiting
api.Use(rateLimitMiddleware(10, 20)) // 10 RPS with burst of 20
```

## Best Practices

### 1. Error Handling
```go
// Always provide meaningful error messages
func (s *Server) handleError(w http.ResponseWriter, err error, message string) {
    log.Printf("API Error: %s - %v", message, err)
    
    response := map[string]string{
        "error":   message,
        "details": err.Error(),
    }
    
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusInternalServerError)
    json.NewEncoder(w).Encode(response)
}
```

### 2. Request Validation
```go
// Validate function names
func isValidFunctionName(name string) bool {
    if len(name) == 0 || len(name) > 100 {
        return false
    }
    
    // Go identifier rules: letter or underscore, followed by letters, digits, underscores
    matched, _ := regexp.MatchString(`^[a-zA-Z_][a-zA-Z0-9_]*$`, name)
    return matched
}
```

### 3. Caching
```go
// Implement response caching
var sourceCodeCache = make(map[string]CacheEntry)
var cacheMutex sync.RWMutex

type CacheEntry struct {
    Value     string
    ExpiresAt time.Time
}

func getCachedSourceCode(functionName string) (string, bool) {
    cacheMutex.RLock()
    defer cacheMutex.RUnlock()
    
    entry, exists := sourceCodeCache[functionName]
    if !exists || time.Now().After(entry.ExpiresAt) {
        return "", false
    }
    
    return entry.Value, true
}
```

## Next Steps

- **Advanced Queries**: Learn complex analysis patterns in [Advanced Queries](./08-advanced-queries.md)
- **Configuration**: Optimize for production in [Configuration Reference](./10-configuration-reference.md)
- **Troubleshooting**: Debug integration issues in [Troubleshooting](./11-troubleshooting.md)

This integration guide provides the foundation for building powerful code intelligence tools on top of CodeGraph. The precise source code retrieval and rich graph data make it ideal for LLM-powered development assistance, automated code review, and comprehensive code analysis.