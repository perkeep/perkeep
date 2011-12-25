package gocheck

import (
	"os"
)

func PrintLine(filename string, line int) (string, os.Error) {
	return printLine(filename, line)
}

func Indent(s, with string) string {
	return indent(s, with)
}
