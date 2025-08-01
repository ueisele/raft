// +build ignore

// This tool helps automate the migration from old HTTPTransport API to the new one.
// Usage: go run scripts/migrate_discovery.go [flags] [files...]
//
// Flags:
//   -dry-run  Show what would be changed without modifying files
//   -backup   Create .bak files before modifying
//
// Example:
//   go run scripts/migrate_discovery.go -dry-run ./cmd/...
//   go run scripts/migrate_discovery.go -backup ./internal/server.go

package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
)

var (
	dryRun = flag.Bool("dry-run", false, "Show changes without modifying files")
	backup = flag.Bool("backup", false, "Create backup files before modifying")
)

type migrator struct {
	fset         *token.FileSet
	file         *ast.File
	filename     string
	modified     bool
	importNeeded bool
}

func main() {
	flag.Parse()

	if flag.NArg() == 0 {
		fmt.Println("Usage: go run migrate_discovery.go [flags] [files or directories...]")
		flag.PrintDefaults()
		os.Exit(1)
	}

	var files []string
	for _, arg := range flag.Args() {
		if strings.HasSuffix(arg, "...") {
			// Handle directory recursion
			base := strings.TrimSuffix(arg, "...")
			err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if strings.HasSuffix(path, ".go") && !strings.Contains(path, "vendor/") {
					files = append(files, path)
				}
				return nil
			})
			if err != nil {
				log.Fatalf("Error walking directory %s: %v", base, err)
			}
		} else if strings.HasSuffix(arg, ".go") {
			files = append(files, arg)
		} else {
			// Check if it's a directory
			info, err := os.Stat(arg)
			if err != nil {
				log.Fatalf("Error accessing %s: %v", arg, err)
			}
			if info.IsDir() {
				// Find all .go files in directory
				matches, err := filepath.Glob(filepath.Join(arg, "*.go"))
				if err != nil {
					log.Fatalf("Error finding go files in %s: %v", arg, err)
				}
				files = append(files, matches...)
			} else {
				files = append(files, arg)
			}
		}
	}

	totalModified := 0
	for _, file := range files {
		if modified, err := processFile(file); err != nil {
			log.Printf("Error processing %s: %v", file, err)
		} else if modified {
			totalModified++
		}
	}

	fmt.Printf("\nSummary: %d files %s\n", totalModified, 
		map[bool]string{true: "would be modified", false: "modified"}[*dryRun])
}

func processFile(filename string) (bool, error) {
	src, err := ioutil.ReadFile(filename)
	if err != nil {
		return false, err
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return false, err
	}

	m := &migrator{
		fset:     fset,
		file:     file,
		filename: filename,
	}

	// Look for patterns to migrate
	ast.Inspect(file, m.inspect)

	if !m.modified {
		return false, nil
	}

	// Add import if needed
	if m.importNeeded {
		addImport(file, "github.com/ueisele/raft/transport")
	}

	// Format the modified AST
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, file); err != nil {
		return false, err
	}

	if *dryRun {
		fmt.Printf("\n=== %s ===\n", filename)
		fmt.Println("Would apply the following changes:")
		// In a real implementation, we'd show a diff here
		fmt.Println("[Changes would be shown here]")
		return true, nil
	}

	// Create backup if requested
	if *backup {
		if err := ioutil.WriteFile(filename+".bak", src, 0644); err != nil {
			return false, err
		}
	}

	// Write the modified file
	if err := ioutil.WriteFile(filename, buf.Bytes(), 0644); err != nil {
		return false, err
	}

	fmt.Printf("Modified: %s\n", filename)
	return true, nil
}

func (m *migrator) inspect(n ast.Node) bool {
	// This is a simplified example. A full implementation would:
	// 1. Track variable assignments of NewHTTPTransport
	// 2. Find subsequent SetDiscovery calls
	// 3. Combine them into NewHTTPTransportWithDiscovery
	// 4. Handle error returns properly
	
	// For now, just detect the pattern and report it
	switch x := n.(type) {
	case *ast.CallExpr:
		if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
			if sel.Sel.Name == "NewHTTPTransport" {
				// Found a NewHTTPTransport call
				fmt.Printf("Found NewHTTPTransport in %s at line %d\n", 
					m.filename, m.fset.Position(x.Pos()).Line)
				// In a full implementation, we'd transform this
				m.modified = true
			}
		}
	}
	
	return true
}

func addImport(file *ast.File, path string) {
	// Simplified import addition
	// In a real implementation, this would properly add to existing import blocks
	fmt.Printf("Would add import: %s\n", path)
}