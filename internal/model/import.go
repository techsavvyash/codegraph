package models

// PackageImport represents an import statement from one file/package to another
type PackageImport struct {
	SourceFile    string   // File containing the import
	TargetPackage string   // Package being imported
	ImportedNames []string // Specific symbols imported (if available)
	IsExternal    bool     // Whether this is an external package
}
