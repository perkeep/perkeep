package main

import (
	"os"
)

func errorIsNoEnt(err os.Error) bool {
	if err == os.ENOENT {
		return true
	}
	switch err.(type) {
	case *os.PathError:
		perr := err.(*os.PathError)
		return errorIsNoEnt(perr.Error)
	}
	return false
}
