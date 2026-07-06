# RFC 008: Semantic Linking of Documents to Code via Graph Embeddings and LLM Analysis

| Field | Value |
|-------|-------|
| **Status** | Withdrawn |
| **Author** | Code Graph Team |

> **Status note:** The LLM/embedding-driven document-linking subsystem this RFC
> specifies (Qdrant vector search, LLM-based feature-to-code matching) was
> removed per RFC-004 (doc-linking declared out of scope). A redesigned docs
> plane is specified in RFC-005 §7 and scheduled in RFC-006 Phase 4.

Problem Statement:  
The current document indexing pipeline creates links between :Document/:Feature nodes and code symbols primarily through explicit mentions (:MENTIONS relationship) or direct naming conventions that an LLM can easily identify. This approach is effective but limited. It fails to connect a document that describes a high-level business logic (e.g., "Calculate the user's final invoice amount by applying regional taxes and any applicable promotional discounts") to the specific cluster of functions that implement this logic, especially if those functions have non-obvious names (e.g., compute\_final\_cost, apply\_vat, get\_promo\_value). This is a semantic gap between the "what" described in the document and the "how" implemented in the code.54  
Proposed Solution:  
This proposal introduces a multi-stage, LLM-driven process to create semantic links between document features and the code subgraphs that implement them. This process does not require a change to the existing graph schema; rather, it leverages the rich structure of the call graph (:CALLS) and data-flow graph (:FLOWS\_TO) to create semantically meaningful representations of code logic, which can then be compared to the semantics of the requirements. The core idea is to translate both the natural language requirement and the code's logic into a common format—vector embeddings—and use similarity search to find matches.55  
The process is as follows:

Code snippet

graph TD  
    subgraph "Document Processing"  
        A \--\>|1. LLM Extraction| B((":Feature\<br\>'Calculate final invoice...'"));  
        B \--\>|2. Embedding Model| C\["Feature Embedding\<br\>\[0.1, 0.8,...\]"\];  
    end

    subgraph "Code Graph Analysis"  
        D\["Code Property Graph"\] \--\>|3. Identify Candidate Subgraph| E;  
        E \--\>|4. LLM Summarization| F;  
        F \--\>|5. Embedding Model| G\["Code Logic Embedding\<br\>\[0.12, 0.79,...\]"\];  
    end

    subgraph "Semantic Linking"  
        C \--\>|6. Vector Similarity Search| H{"Find Closest Code Embedding"};  
        G \--\> H;  
        H \--\>|7. LLM Validation| I{"LLM: Does this code implement this feature?"};  
        I \-- "Yes" \--\>|8. Create Relationship| J-\>(:Feature)"\];  
    end

1. **Feature Embedding:** During the document ingestion pipeline, for each :Feature node extracted, use an embedding model to generate a vector embedding from its description property. This vector captures the semantic intent of the requirement.56  
2. **Code Subgraph Summarization and Embedding:**  
   * **Candidate Identification:** For a given :Feature, identify potential entry points in the code graph. This can be bootstrapped with a keyword search based on the feature description.  
   * **Subgraph Extraction:** Starting from a candidate entry point (e.g., an API handler function), traverse the existing graph along :CALLS and :FLOWS\_TO relationships to a predefined depth. This extracted subgraph represents a self-contained piece of business logic.57  
   * **LLM-Powered Summarization:** Feed the source code of all functions within this subgraph to an LLM. Prompt the LLM to generate a concise, natural language summary that describes the *purpose and behavior* of the code logic.58 This transforms the syntactic code into a semantic description.  
   * **Code Logic Embedding:** Generate a vector embedding from this LLM-generated summary. This embedding now represents the semantics of the code subgraph in the same vector space as the feature embeddings.57 This embedding can be stored as a property on the entry-point  
     :Function or :Method node.  
3. **Semantic Search and Validation:**  
   * **Vector Search:** To find the code that implements a specific feature, perform a vector similarity search (e.g., cosine similarity) between the feature's embedding and the pre-computed code logic embeddings across the codebase.59 The code subgraphs with the highest similarity scores are the most likely implementations.  
   * **LLM as a Judge:** As a final validation step, the top candidate matches can be presented to an LLM. The model receives the original feature description and the summary of the candidate code subgraph and is asked to make a final determination: "Does this code logic accurately implement the specified feature?".54  
4. **Graph Link Creation:** If the LLM validation is positive, create an :IMPLEMENTS relationship in the graph, connecting the entry-point function of the code subgraph to the corresponding :Feature node.

This advanced technique moves beyond simple lexical matching to a truly semantic understanding, enabling the system to trace high-level, abstract requirements directly to the complex web of functions that bring them to life.