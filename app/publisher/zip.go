/*
Copyright 2013 The Camlistore Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"archive/zip"
	"crypto/sha1"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"path"
	"sort"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
	"camlistore.org/pkg/types/camtypes"
)

type zipHandler struct {
	fetcher blob.Fetcher
	cl      client // Used for search and describe requests.
	// root is the "parent" permanode of everything to zip.
	// Either a directory permanode, or a permanode with members.
	root blob.Ref
	// Optional name to use in the response header
	filename string
}

// blobFile contains all the information we need about
// a file blob to add the corresponding file to a zip.
type blobFile struct {
	blobRef blob.Ref
	// path is the full path of the file from the root of the zip.
	// slashes are always forward slashes, per the zip spec.
	path string
}

type sortedFiles []*blobFile

func (s sortedFiles) Less(i, j int) bool { return s[i].path < s[j].path }
func (s sortedFiles) Len() int           { return len(s) }
func (s sortedFiles) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

func (zh *zipHandler) describeMembers(br blob.Ref) (*search.DescribeResponse, error) {
	res, err := zh.cl.Query(&search.SearchQuery{
		Constraint: &search.Constraint{
			BlobRefPrefix: br.String(),
			CamliType:     "permanode",
		},
		Describe: &search.DescribeRequest{
			Depth: 1,
			Rules: []*search.DescribeRule{
				{
					Attrs: []string{"camliContent", "camliContentImage", "camliMember"},
				},
			},
		},
		Limit: -1,
	})
	if err != nil {
		return nil, fmt.Errorf("Could not describe %v: %v", br, err)
	}
	if res == nil || res.Describe == nil {
		return nil, fmt.Errorf("no describe result for %v", br)
	}
	return res.Describe, nil
}

// blobList returns the list of file blobs "under" dirBlob.
// It traverses permanode directories and permanode with members (collections).
func (zh *zipHandler) blobList(dirPath string, dirBlob blob.Ref) ([]*blobFile, error) {
	//	dr := zh.search.NewDescribeRequest()
	//	dr.Describe(dirBlob, 3)
	//	res, err := dr.Result()
	//	if err != nil {
	//		return nil, fmt.Errorf("Could not describe %v: %v", dirBlob, err)
	//	}
	res, err := zh.describeMembers(dirBlob)
	if err != nil {
		return nil, err
	}

	described := res.Meta[dirBlob.String()]
	members := described.Members()
	dirBlobPath, _, isDir := described.PermanodeDir()
	if len(members) == 0 && !isDir {
		return nil, nil
	}
	var list []*blobFile
	if isDir {
		dirRoot := dirBlobPath[1]
		children, err := zh.blobsFromDir("/", dirRoot)
		if err != nil {
			return nil, fmt.Errorf("Could not get list of blobs from %v: %v", dirRoot, err)
		}
		list = append(list, children...)
		return list, nil
	}
	for _, member := range members {
		if fileBlobPath, fileInfo, ok := getFileInfo(member.BlobRef, res.Meta); ok {
			// file
			list = append(list,
				&blobFile{fileBlobPath[1], path.Join(dirPath, fileInfo.FileName)})
			continue
		}
		if dirBlobPath, dirInfo, ok := getDirInfo(member.BlobRef, res.Meta); ok {
			// directory
			newZipRoot := dirBlobPath[1]
			children, err := zh.blobsFromDir(
				path.Join(dirPath, dirInfo.FileName), newZipRoot)
			if err != nil {
				return nil, fmt.Errorf("Could not get list of blobs from %v: %v", newZipRoot, err)
			}
			list = append(list, children...)
			// TODO(mpl): we assume a directory permanode does not also have members.
			// I know there is nothing preventing it, but does it make any sense?
			continue
		}
		// it might have members, so recurse
		// If it does have members, we must consider it as a pseudo dir,
		// so we can build a fullpath for each of its members.
		// As a dir name, we're using its title if it has one, its (shortened)
		// blobref otherwise.
		pseudoDirName := member.Title()
		if pseudoDirName == "" {
			pseudoDirName = member.BlobRef.DigestPrefix(10)
		}
		fullpath := path.Join(dirPath, pseudoDirName)
		moreMembers, err := zh.blobList(fullpath, member.BlobRef)
		if err != nil {
			return nil, fmt.Errorf("Could not get list of blobs from %v: %v", member.BlobRef, err)
		}
		list = append(list, moreMembers...)
	}
	return list, nil
}

// blobsFromDir returns the list of file blobs in directory dirBlob.
// It only traverses permanode directories.
func (zh *zipHandler) blobsFromDir(dirPath string, dirBlob blob.Ref) ([]*blobFile, error) {
	var list []*blobFile
	dr, err := schema.NewDirReader(zh.fetcher, dirBlob)
	if err != nil {
		return nil, fmt.Errorf("Could not read dir blob %v: %v", dirBlob, err)
	}
	ent, err := dr.Readdir(-1)
	if err != nil {
		return nil, fmt.Errorf("Could not read dir entries: %v", err)
	}
	for _, v := range ent {
		fullpath := path.Join(dirPath, v.FileName())
		switch v.CamliType() {
		case "file":
			list = append(list, &blobFile{v.BlobRef(), fullpath})
		case "directory":
			children, err := zh.blobsFromDir(fullpath, v.BlobRef())
			if err != nil {
				return nil, fmt.Errorf("Could not get list of blobs from %v: %v", v.BlobRef(), err)
			}
			list = append(list, children...)
		}
	}
	return list, nil
}

// renameDuplicates goes through bf to check for duplicate filepaths.
// It renames duplicate filepaths and returns a new slice, sorted by
// file path.
func renameDuplicates(bf []*blobFile) sortedFiles {
	noDup := make(map[string]blob.Ref)
	// use a map to detect duplicates and rename them
	for _, file := range bf {
		if _, ok := noDup[file.path]; ok {
			// path already exists, so rename
			suffix := 0
			var newname string
			for {
				suffix++
				ext := path.Ext(file.path)
				newname = fmt.Sprintf("%s(%d)%s",
					file.path[:len(file.path)-len(ext)], suffix, ext)
				if _, ok := noDup[newname]; !ok {
					break
				}
			}
			noDup[newname] = file.blobRef
		} else {
			noDup[file.path] = file.blobRef
		}
	}

	// reinsert in a slice and sort it
	var sorted sortedFiles
	for p, b := range noDup {
		sorted = append(sorted, &blobFile{path: p, blobRef: b})
	}
	sort.Sort(sorted)
	return sorted
}

// ServeHTTP streams a zip archive of all the files "under"
// zh.root. That is, all the files pointed by file permanodes,
// which are directly members of zh.root or recursively down
// directory permanodes and permanodes members.
// To build the fullpath of a file in a collection, it uses
// the collection title if present, its blobRef otherwise, as
// a directory name.
func (zh *zipHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	// TODO: use http.ServeContent, so Range requests work and downloads can be resumed.
	// Will require calculating the zip length once first (ideally as cheaply as possible,
	// with dummy counting writer and dummy all-zero-byte-files of a fixed size),
	// and then making a dummy ReadSeeker for ServeContent that can seek to the end,
	// and then seek back to the beginning, but then seeks forward make it remember
	// to skip that many bytes from the archive/zip writer when answering Reads.
	if !httputil.IsGet(req) {
		http.Error(rw, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	bf, err := zh.blobList("/", zh.root)
	if err != nil {
		log.Printf("Could not serve zip for %v: %v", zh.root, err)
		http.Error(rw, "Server error", http.StatusInternalServerError)
		return
	}
	blobFiles := renameDuplicates(bf)

	// TODO(mpl): streaming directly won't work on appengine if the size goes
	// over 32 MB. Deal with that.
	h := rw.Header()
	h.Set("Content-Type", "application/zip")
	filename := zh.filename
	if filename == "" {
		filename = "download.zip"
	}
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	zw := zip.NewWriter(rw)
	etag := sha1.New()
	for _, file := range blobFiles {
		etag.Write([]byte(file.blobRef.String()))
	}
	h.Set("Etag", fmt.Sprintf(`"%x"`, etag.Sum(nil)))

	for _, file := range blobFiles {
		fr, err := schema.NewFileReader(zh.fetcher, file.blobRef)
		if err != nil {
			log.Printf("Can not add %v in zip, not a file: %v", file.blobRef, err)
			http.Error(rw, "Server error", http.StatusInternalServerError)
			return
		}
		f, err := zw.CreateHeader(
			&zip.FileHeader{
				Name:   file.path,
				Method: zip.Store,
			})
		if err != nil {
			log.Printf("Could not create %q in zip: %v", file.path, err)
			http.Error(rw, "Server error", http.StatusInternalServerError)
			return
		}
		_, err = io.Copy(f, fr)
		fr.Close()
		if err != nil {
			log.Printf("Could not zip %q: %v", file.path, err)
			return
		}
	}
	err = zw.Close()
	if err != nil {
		log.Printf("Could not close zipwriter: %v", err)
		return
	}
}

// TODO(mpl): refactor with getFileInfo
func getDirInfo(item blob.Ref, peers map[string]*search.DescribedBlob) (path []blob.Ref, di *camtypes.FileInfo, ok bool) {
	described := peers[item.String()]
	if described == nil ||
		described.Permanode == nil ||
		described.Permanode.Attr == nil {
		return
	}
	contentRef := described.Permanode.Attr.Get("camliContent")
	if contentRef == "" {
		return
	}
	if cdes := peers[contentRef]; cdes != nil && cdes.Dir != nil {
		return []blob.Ref{described.BlobRef, cdes.BlobRef}, cdes.Dir, true
	}
	return
}
