# LLM Implementation for RFC-002

## Overview

This document describes the LLM (Large Language Model) implementation for the CodeGraph RFC-002 specification, which enables semantic linking of features to code using true LLM-powered text generation and validation.

## Architecture

### Components

#### 1. **LLMService Interface** (`pkg/search/llm_service.go`)

Defines the contract for LLM text generation:

```go
type LLMService interface {
    GenerateText(ctx context.Context, prompt string) (string, error)
    GenerateTextWithSystemPrompt(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}
```

#### 2. **GeminiLLMService** (`pkg/search/llm_service.go`)

Implements LLM text generation using Google's Gemini API:

- **Model**: `gemini-1.5-flash` (fast, efficient for code analysis)
- **Temperature**: 0.2 (deterministic outputs)
- **Max Tokens**: 1024
- **Features**:
  - System prompts for guided generation
  - Configurable safety settings
  - JSON parsing for structured outputs

#### 3. **Enhanced CodeSubgraphSummarizer** (`pkg/search/code_summarizer.go`)

Now supports **true LLM-powered code summarization**:

**Before (Heuristic)**:
```go
summary = "Code subgraph starting from FunctionName involving 5 functions"
```

**After (LLM-Generated)**:
```go
// Sends actual code to LLM for analysis
summary = "This code implements user authentication by validating credentials
           against a database, generating JWT tokens, and managing session state."
```

**Process**:
1. Extracts code subgraph (functions + signatures + docstrings)
2. Builds a prompt requesting business-level summary
3. Sends to Gemini LLM for text generation
4. Returns natural language description of code purpose
5. Generates embedding from the summary

**Confidence Scores**:
- LLM-generated: 0.9
- Heuristic fallback: 0.7

#### 4. **Enhanced LLMValidator** (`pkg/search/llm_validator.go`)

Now supports **true LLM-as-judge validation**:

**Before (Heuristic)**:
```go
// Rule-based: keyword overlap + function name analysis
isMatch = (keywordOverlap > 0.3 && meaningfulNames > 0.5)
```

**After (LLM Validation)**:
```go
// Asks LLM: "Does this code implement this feature?"
response = LLM.GenerateText(validationPrompt)
// Expected: {"isMatch": true, "confidence": 0.85, "reasoning": "..."}
```

**Process**:
1. Creates validation prompt with feature description + code summary
2. Sends to LLM with system prompt: "You are a code analysis expert..."
3. LLM responds with JSON containing match decision + reasoning
4. Parses response (with fallback to textual parsing)
5. Returns structured ValidationResult

**Validation Methods**:
- Primary: LLM-based semantic analysis
- Fallback 1: Heuristic + embedding similarity
- Fallback 2: Pure heuristic rules

## CLI Integration

### Feature Linking Command

```bash
./bin/codegraph link features \
  --gemini \
  --api-key="YOUR_GEMINI_API_KEY" \
  --max-candidates=10 \
  --min-confidence=0.6 \
  --dry-run
```

**With Gemini**:
- ✅ Embedding generation
- ✅ LLM code summarization
- ✅ LLM validation

**Without Gemini** (generic embedding service):
- ✅ Embedding generation
- ⚠️  Heuristic summarization
- ⚠️  Heuristic validation

## Implementation Details

### 1. Code Summarization Prompt

```
Analyze the following code subgraph and provide a concise, natural language summary
that describes the PURPOSE and BEHAVIOR of this code logic.
Focus on WHAT the code accomplishes from a business/functional perspective, not HOW it's implemented.

ENTRY POINT: authenticateUser

FUNCTIONS IN SUBGRAPH:
1. func authenticateUser(username, password string) (*User, error)
   Doc: Authenticates a user with credentials
   Code preview:
   func authenticateUser(username, password string) (*User, error) {
       user, err := db.FindUserByUsername(username)
       if err != nil {
           return nil, err
       }
   ... (15 more lines)

2. func validatePassword(hash, password string) bool
   ...

Provide a 2-3 sentence summary that captures the core business logic and purpose of this code subgraph.
Focus on the high-level business value, not implementation details.
```

### 2. Validation Prompt

```
TASK: Determine if the following code logic implements the specified feature requirement.

FEATURE REQUIREMENT:
Name: User Authentication
Description: Allow users to log in using username and password with JWT token generation

CODE IMPLEMENTATION:
Entry Point: authenticateUser
Code Summary: This code implements user authentication by validating credentials
              against a database, generating JWT tokens, and managing session state.
Functions Involved: 5
Key Functions:
- func authenticateUser(username, password string) (*User, error)
  Doc: Authenticates a user with credentials
- func generateJWT(user *User) (string, error)
  Doc: Generates JWT token for authenticated user
...

QUESTION: Does this code logic accurately implement the specified feature requirement?
Consider:
1. Does the code's purpose align with the feature's intent?
2. Are the key behaviors described in the feature present in the code?
3. Is this a primary implementation or just tangentially related?

RESPOND: Yes/No with brief reasoning.
```

### 3. LLM Response Parsing

**Ideal JSON Response**:
```json
{
  "isMatch": true,
  "confidence": 0.85,
  "reasoning": "The code directly implements user authentication with password validation and JWT generation, matching the feature requirements."
}
```

**Fallback for Text Responses**:
- Detects "yes/no" indicators
- Extracts confidence percentages
- Uses full response as reasoning

## Testing

### Prerequisites

1. **Neo4j Database**:
   ```bash
   make docker-up
   ```

2. **Gemini API Key**:
   ```bash
   export GEMINI_API_KEY="your-key-here"
   ```

### Test Workflow

1. **Index Code with Embeddings**:
   ```bash
   ./bin/codegraph index project . \
     --service="codegraph" \
     --version="v1.0.0" \
     --generate-embeddings \
     --embedding-gemini \
     --embedding-api-key="$GEMINI_API_KEY"
   ```

2. **Index Documents/Features**:
   ```bash
   ./bin/codegraph index docs docs/ \
     --service="codegraph" \
     --version="v1.0.0" \
     --generate-embeddings \
     --embedding-gemini \
     --embedding-api-key="$GEMINI_API_KEY"
   ```

3. **Run LLM-Powered Feature Linking**:
   ```bash
   ./bin/codegraph link features \
     --gemini \
     --api-key="$GEMINI_API_KEY" \
     --max-candidates=5 \
     --min-confidence=0.6 \
     --verbose
   ```

### Expected Output

```
🚀 Starting RFC-002 Feature Linking Process...

🧠 Using Gemini embedding service (model: gemini-embedding-001)
🤖 Using Gemini LLM service for text generation and validation
📊 Using minimum confidence threshold: 0.60
🎯 Maximum candidates per feature: 5

Linking feature to code: Document Indexing
Found 8 candidate entry points for feature Document Indexing
Using LLM service for code summarization
Generated LLM code summary: This code processes markdown documents by extracting features through pattern matching and natural language processing. It creates document nodes in Neo4j and establishes relationships to identified code symbols. (confidence: 0.90)
Using LLM service for validation
LLM validation result: isMatch=true, confidence=0.850, reasoning=The code implements document indexing with feature extraction and graph storage, aligning with the feature requirements.

🎯 FEATURE: Document Indexing
   Description: Index documentation files and extract features
   Candidates Found: 8
   Candidates Validated: 5
   IMPLEMENTS Links Created: 3
   📝 IMPLEMENTATIONS:
      1. IndexDocument (confidence: 0.850)
         Summary: This code processes markdown documents by extracting features...
         Subgraph Size: 4 functions
         Validation: LLM validation: The code implements document indexing... (confidence: 0.850)

✅ Feature linking completed successfully!
```

## Performance Characteristics

### API Calls

For each feature:
- **Candidate Finding**: 1 vector search query
- **Summarization**: N LLM calls (where N = number of candidates, typically 5-10)
- **Validation**: N LLM calls (one per candidate)

**Total**: ~10-20 LLM API calls per feature

### Optimization Strategies

1. **Batching**: Future enhancement to batch multiple summaries/validations
2. **Caching**: Code summaries can be cached on Function nodes
3. **Parallel Processing**: Independent candidates can be processed concurrently
4. **Early Termination**: Stop after finding K high-confidence matches

## Cost Estimation

Using Gemini 1.5 Flash pricing:
- **Input**: $0.075 per 1M tokens
- **Output**: $0.30 per 1M tokens

Per feature (10 LLM calls, ~2K tokens input, ~200 tokens output each):
- Input: 20K tokens = $0.0015
- Output: 2K tokens = $0.0006
- **Total**: ~$0.002 per feature

For 100 features: **~$0.20**

## RFC-002 Compliance

| Requirement | Status | Implementation |
|-------------|--------|----------------|
| Feature embedding generation | ✅ Complete | Gemini Embedding API |
| Code subgraph extraction | ✅ Complete | Graph traversal via CALLS/FLOWS_TO |
| **LLM code summarization** | ✅ **Complete** | **Gemini 1.5 Flash text generation** |
| **LLM validation** | ✅ **Complete** | **Gemini as judge with JSON output** |
| IMPLEMENTS relationship | ✅ Complete | With confidence + metadata |
| Vector similarity search | ✅ Complete | Cosine similarity |

**Status**: **100% RFC-002 Compliant** with full LLM integration

## Fallback Behavior

The system gracefully degrades when LLM is unavailable:

```
LLM Available    → High-quality semantic analysis (confidence: 0.85-0.95)
LLM Unavailable  → Heuristic analysis (confidence: 0.60-0.75)
```

All paths are tested and production-ready.

## Future Enhancements

1. **Streaming**: Support streaming responses for long summaries
2. **Fine-tuning**: Custom models trained on codebase patterns
3. **Multi-modal**: Include code visualization in prompts
4. **Caching Layer**: Redis cache for expensive LLM calls
5. **A/B Testing**: Compare LLM vs heuristic accuracy
