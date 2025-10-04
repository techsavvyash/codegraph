CREATE (f:Feature {
    id: "test-feature-1",
    name: "Semantic Feature Linking",
    description: "Link features to code implementations using semantic analysis and LLM validation",
    sourcePath: "test/feature.md",
    sourceLineStart: 1,
    sourceLineEnd: 10,
    createdAt: datetime(),
    updatedAt: datetime()
})
RETURN f