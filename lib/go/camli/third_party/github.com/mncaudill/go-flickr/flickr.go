package flickr

import (
	"io"
	"os"
	"fmt"
	"net"
	"http"
	"sort"
	"bufio"
	"bytes"
	"io/ioutil"
	"crypto/md5"
)

const (
	endpoint        = "http://api.flickr.com/services/rest/?"
	uploadEndpoint  = "http://api.flickr.com/services/upload/"
	replaceEndpoint = "http://api.flickr.com/services/replace/"
	apiHost         = "api.flickr.com"
)

type Request struct {
	ApiKey string
	Method string
	Args   map[string]string
}

type nopCloser struct {
	io.Reader
}

func (nopCloser) Close() os.Error { return nil }

type Error string

func (e Error) String() string {
	return string(e)
}

func (request *Request) Sign(secret string) {
	args := request.Args

	// Remove api_sig
	args["api_sig"] = "", false

	sorted_keys := make([]string, len(args)+2)

	args["api_key"] = request.ApiKey
	args["method"] = request.Method

	// Sort array keys
	i := 0
	for k := range args {
		sorted_keys[i] = k
		i++
	}
	sort.SortStrings(sorted_keys)

	// Build out ordered key-value string prefixed by secret
	s := secret
	for _, key := range sorted_keys {
		if args[key] != "" {
			s += fmt.Sprintf("%s%s", key, args[key])
		}
	}

	// Since we're only adding two keys, it's easier 
	// and more space-efficient to just delete them
	// them copy the whole map
	args["api_key"] = "", false
	args["method"] = "", false

	// Have the full string, now hash
	hash := md5.New()
	hash.Write([]byte(s))

	// Add api_sig as one of the args
	args["api_sig"] = fmt.Sprintf("%x", hash.Sum())
}

func (request *Request) URL() string {
	args := request.Args

	args["api_key"] = request.ApiKey
	args["method"] = request.Method

	s := endpoint + encodeQuery(args)
	return s
}

func (request *Request) Execute() (response string, ret os.Error) {
	if request.ApiKey == "" || request.Method == "" {
		return "", Error("Need both API key and method")
	}

	s := request.URL()

	res, _, err := http.Get(s)
	defer res.Body.Close()
	if err != nil {
		return "", err
	}

	body, _ := ioutil.ReadAll(res.Body)
	return string(body), nil
}

func encodeQuery(args map[string]string) string {
	i := 0
	s := bytes.NewBuffer(nil)
	for k, v := range args {
		if i != 0 {
			s.WriteString("&")
		}
		i++
		s.WriteString(k + "=" + http.URLEscape(v))
	}
	return s.String()
}

func (request *Request) buildPost(url string, filename string, filetype string) (*http.Request, os.Error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}

	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	f_size := stat.Size

	request.Args["api_key"] = request.ApiKey

	boundary, end := "----###---###--flickr-go-rules", "\r\n"

	// Build out all of POST body sans file
	header := bytes.NewBuffer(nil)
	for k, v := range request.Args {
		header.WriteString("--" + boundary + end)
		header.WriteString("Content-Disposition: form-data; name=\"" + k + "\"" + end + end)
		header.WriteString(v + end)
	}
	header.WriteString("--" + boundary + end)
	header.WriteString("Content-Disposition: form-data; name=\"photo\"; filename=\"photo.jpg\"" + end)
	header.WriteString("Content-Type: " + filetype + end + end)

	footer := bytes.NewBufferString(end + "--" + boundary + "--" + end)

	body_len := int64(header.Len()) + int64(footer.Len()) + f_size

	r, w := io.Pipe()
	go func() {
		pieces := []io.Reader{header, f, footer}

		for _, k := range pieces {
			_, err = io.Copy(w, k)
			if err != nil {
				w.CloseWithError(nil)
				return
			}
		}
		f.Close()
		w.Close()
	}()

	http_header := make(http.Header)
	http_header.Add("Content-Type", "multipart/form-data; boundary="+boundary)

	postRequest := &http.Request{
		Method:        "POST",
		RawURL:        url,
		Host:          apiHost,
		Header:        http_header,
		Body:          r,
		ContentLength: body_len,
	}
	return postRequest, nil
}

// Example: 
// r.Upload("thumb.jpg", "image/jpeg")
func (request *Request) Upload(filename string, filetype string) (response string, err os.Error) {
	postRequest, err := request.buildPost(uploadEndpoint, filename, filetype)
	if err != nil {
		return "", err
	}

	return sendPost(postRequest)
}

func (request *Request) Replace(filename string, filetype string) (response string, err os.Error) {
	postRequest, err := request.buildPost(replaceEndpoint, filename, filetype)
	if err != nil {
		return "", err
	}
	return sendPost(postRequest)
}

func sendPost(postRequest *http.Request) (body string, err os.Error) {
	// Create and use TCP connection (lifted mostly wholesale from http.send)
	conn, err := net.Dial("tcp", "api.flickr.com:80")
	defer conn.Close()

	if err != nil {
		return "", err
	}
	postRequest.Write(conn)

	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, postRequest.Method)
	if err != nil {
		return "", err
	}
	rawBody, _ := ioutil.ReadAll(resp.Body)

	return string(rawBody), nil
}
