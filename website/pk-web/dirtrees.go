// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains the code dealing with package directory trees.

package main

import (

	//	"fmt"
	"go/doc"
	"go/parser"
	"go/token"
	"log"
	"os"
	pathpkg "path"
	"strings"
)

// Conventional name for directories containing test data.
// Excluded from directory trees.
const testdataDirName = "testdata"

type Directory struct {
	Depth    int
	Path     string       // directory path; includes Name
	Name     string       // directory name
	HasPkg   bool         // true if the directory contains at least one package
	Synopsis string       // package documentation, if any
	Dirs     []*Directory // subdirectories
}

// dirEntry defines functions for determining whether
// entry is a directory or a file.
// The interface is a common part of fs.FileInfo and fs.DirEntry.
type dirEntry interface {
	// Name is returns the base name of the file or subdirectory.
	Name() string
	// IsDir returns whether the entry describes a directory.
	IsDir() bool
}

func isGoFile(de dirEntry) bool {
	name := de.Name()
	return !de.IsDir() &&
		len(name) > 0 && name[0] != '.' && // ignore .files
		pathpkg.Ext(name) == ".go"
}

func isPkgFile(de dirEntry) bool {
	return isGoFile(de) &&
		!strings.HasSuffix(de.Name(), "_test.go") && // ignore test files
		!strings.HasSuffix(de.Name(), fileembedPattern)
}

func isPkgDir(de dirEntry) bool {
	name := de.Name()
	return de.IsDir() && len(name) > 0 &&
		name[0] != '_' && name[0] != '.' // ignore _files and .files
}

type treeBuilder struct {
	maxDepth int
}

func (b *treeBuilder) newDirTree(fset *token.FileSet, path, name string, depth int) *Directory {
	if name == testdataDirName {
		return nil
	}

	if depth >= b.maxDepth {
		// return a dummy directory so that the parent directory
		// doesn't get discarded just because we reached the max
		// directory depth
		return &Directory{
			Depth: depth,
			Path:  path,
			Name:  name,
		}
	}

	list, err := os.ReadDir(path)
	if err != nil {
		log.Printf("Could not read %v\n", path)
		return nil
	}

	// determine number of subdirectories and if there are package files
	ndirs := 0
	hasPkgFiles := false
	var synopses [4]string // prioritized package documentation (0 == highest priority)
	for _, d := range list {
		switch {
		case isPkgDir(d):
			ndirs++
		case isPkgFile(d):
			// looks like a package file, but may just be a file ending in ".go";
			// don't just count it yet (otherwise we may end up with hasPkgFiles even
			// though the directory doesn't contain any real package files - was bug)
			if synopses[0] == "" {
				// no "optimal" package synopsis yet; continue to collect synopses
				src, err := os.ReadFile(pathpkg.Join(path, d.Name()))
				if err != nil {
					log.Printf("Could not read %v\n", pathpkg.Join(path, d.Name()))
					continue
				}
				file, err := parser.ParseFile(fset, pathpkg.Join(path, d.Name()),
					src, parser.ParseComments|parser.PackageClauseOnly)
				if err == nil {
					hasPkgFiles = true
					if file.Doc != nil {
						// prioritize documentation
						i := -1
						switch file.Name.Name {
						case name:
							i = 0 // normal case: directory name matches package name
						case "main":
							i = 2 // directory contains a main package
						default:
							i = 3 // none of the above
						}
						if 0 <= i && i < len(synopses) && synopses[i] == "" {
							// TODO: build a real package? This works for now to appease
							// staticcheck deprecation warnings avbout Go 1.20 removing
							// doc.Synopsis. Instead, just do what doc.Synopsis does itself
							// and use a zero value. It works except for HTML/links in the synopsis.
							var pkg doc.Package
							synopses[i] = pkg.Synopsis(file.Doc.Text())
						}
					}
				}
			}
		}
	}

	// create subdirectory tree
	var dirs []*Directory
	if ndirs > 0 {
		dirs = make([]*Directory, ndirs)
		i := 0
		for _, d := range list {
			if isPkgDir(d) {
				name := d.Name()
				dd := b.newDirTree(fset, pathpkg.Join(path, name), name, depth+1)
				if dd != nil {
					dirs[i] = dd
					i++
				}
			}
		}
		dirs = dirs[0:i]
	}

	// if there are no package files and no subdirectories
	// containing package files, ignore the directory
	if !hasPkgFiles && len(dirs) == 0 {
		return nil
	}

	// select the highest-priority synopsis for the directory entry, if any
	synopsis := ""
	for _, synopsis = range synopses {
		if synopsis != "" {
			break
		}
	}

	return &Directory{
		Depth:    depth,
		Path:     path,
		Name:     name,
		HasPkg:   hasPkgFiles,
		Synopsis: synopsis,
		Dirs:     dirs,
	}
}

// newDirectory creates a new package directory tree with at most maxDepth
// levels, anchored at root. The result tree is pruned such that it only
// contains directories that contain package files or that contain
// subdirectories containing package files (transitively). If a non-nil
// pathFilter is provided, directory paths additionally must be accepted
// by the filter (i.e., pathFilter(path) must be true). If a value >= 0 is
// provided for maxDepth, nodes at larger depths are pruned as well; they
// are assumed to contain package files even if their contents are not known
// (i.e., in this case the tree may contain directories w/o any package files).
func newDirectory(root string, maxDepth int) *Directory {
	// The root could be a symbolic link so use Stat not Lstat.
	d, err := os.Stat(root)
	// If we fail here, report detailed error messages; otherwise
	// is is hard to see why a directory tree was not built.
	switch {
	case err != nil:
		log.Printf("newDirectory(%s): %s", root, err)
		return nil
	case !isPkgDir(d):
		log.Printf("newDirectory(%s): not a package directory", root)
		return nil
	}
	if maxDepth < 0 {
		maxDepth = 1e6 // "infinity"
	}
	b := treeBuilder{maxDepth}
	// the file set provided is only for local parsing, no position
	// information escapes and thus we don't need to save the set
	return b.newDirTree(token.NewFileSet(), root, d.Name(), 0)
}

func (d *Directory) walk(ch chan<- *Directory, skipRoot bool) {
	if d != nil {
		if !skipRoot {
			ch <- d
		}
		for _, c := range d.Dirs {
			c.walk(ch, false)
		}
	}
}

func (d *Directory) iter(skipRoot bool) <-chan *Directory {
	ch := make(chan *Directory)
	go func() {
		defer close(ch)
		d.walk(ch, skipRoot)
	}()
	return ch
}

// DirEntry describes a directory entry. The Depth and Height values
// are useful for presenting an entry in an indented fashion.
type DirEntry struct {
	Depth    int    // >= 0
	Height   int    // = DirList.MaxHeight - Depth, > 0
	Path     string // directory path; includes Name, absolute, with the camli dir as root
	Name     string // directory name
	HasPkg   bool   // true if the directory contains at least one package
	Synopsis string // package documentation, if any
}

type DirList struct {
	MaxHeight int // directory tree height, > 0
	List      []DirEntry
}

// listing creates a (linear) directory listing from a directory tree.
// If skipRoot is set, the root directory itself is excluded from the list.
func (d *Directory) listing(skipRoot bool) *DirList {
	root := d
	if root == nil {
		return nil
	}

	// determine number of entries n and maximum height
	n := 0
	minDepth := 1 << 30 // infinity
	maxDepth := 0
	for d := range root.iter(skipRoot) {
		n++
		if minDepth > d.Depth {
			minDepth = d.Depth
		}
		if maxDepth < d.Depth {
			maxDepth = d.Depth
		}
	}
	maxHeight := maxDepth - minDepth + 1

	if n == 0 {
		return nil
	}

	// create list
	list := make([]DirEntry, n)
	i := 0
	for d := range root.iter(skipRoot) {
		p := &list[i]
		p.Depth = d.Depth - minDepth
		p.Height = maxHeight - p.Depth
		// the suffix is absolute, with the camlistore dir as the root
		idx := strings.LastIndex(d.Path, domainName)
		if idx == -1 {
			log.Fatalf("No \"%s\" in path to file %s", domainName, d.Path)
		}
		suffix := pathpkg.Clean(d.Path[idx+len(domainName):])

		p.Path = suffix
		p.Name = d.Name
		p.HasPkg = d.HasPkg
		p.Synopsis = d.Synopsis
		i++
	}

	return &DirList{maxHeight, list}
}
