package schema

import (
	"bytes"
	"fmt"
	"json"
	"os"
	"strings"
)

func isValidUtf8(s string) bool {
	for _, rune := range []int(s) {
		if rune == 0xfffd {
			return false
		}
	}
	return true
}

var NoCamliVersionErr = os.NewError("No camliVersion key in map")

func MapToCamliJson(m map[string]interface{}) (string, os.Error) {
	version, hasVersion := m["camliVersion"]
	if !hasVersion {
		return "", NoCamliVersionErr
	}
	m["camliVersion"] = 0, false
	jsonBytes, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	m["camliVersion"] = version
	buf := new(bytes.Buffer)
	fmt.Fprintf(buf, "{\"camliVersion\": %v,\n", version)
	buf.Write(jsonBytes[2:])
	return string(buf.Bytes()), nil
}

func NewMapForFileName(fileName string) map[string]interface{} {
	ret := make(map[string]interface{})
	ret["camliVersion"] = 1
	ret["camliType"] = "" // undefined at this point
	
	lastSlash := strings.LastIndex(fileName, "/")
	baseName := fileName[lastSlash+1:]
	if isValidUtf8(baseName) {
		ret["fileName"] = baseName
	} else {
		ret["fileNameBytes"] = []uint8(baseName)
	}
	return ret
}

func NewFileMap(fileName string) (map[string]interface{}, os.Error) {
	ret := NewMapForFileName(fileName)
	// TODO: ...
	return ret, nil
}
