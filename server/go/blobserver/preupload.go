package main

import (
	"container/vector"
	"fmt"
	"http"
	"http_util"
	"os"
)

func handlePreUpload(conn http.ResponseWriter, req *http.Request) {
	if !(req.Method == "POST" && req.URL.Path == "/camli/preupload") {
		http_util.BadRequestError(conn, "Inconfigured handler.")
		return
	}

	req.ParseForm()
	camliVersion := req.FormValue("camliversion")
	if camliVersion == "" {
		http_util.BadRequestError(conn, "No camliversion")
		return
	}
	n := 0
	haveVector := new(vector.Vector)

	haveChan := make(chan *map[string]interface{})
	for {
		key := fmt.Sprintf("blob%v", n+1)
		value := req.FormValue(key)
		if value == "" {
			break
		}
		ref := ParseBlobRef(value)
		if ref == nil {
			http_util.BadRequestError(conn, "Bogus blobref for key "+key)
			return
		}
		if !ref.IsSupported() {
			http_util.BadRequestError(conn, "Unsupported or bogus blobref "+key)
		}
		n++

		// Parallel stat all the files...
		go func() {
			fi, err := os.Stat(ref.FileName())
			if err == nil && fi.IsRegular() {
				info := make(map[string]interface{})
				info["blobRef"] = ref.String()
				info["size"] = fi.Size
				haveChan <- &info
			} else {
				haveChan <- nil
			}
		}()
	}

	if n > 0 {
		for have := range haveChan {
			if have != nil {
				haveVector.Push(have)
			}
			n--
			if n == 0 {
				break
			}
		}
	}

	ret := commonUploadResponse(req)
	ret["alreadyHave"] = haveVector.Copy()
	http_util.ReturnJson(conn, ret)
}

