// +build !linux
// This file is systemdependent because not all versions
// of iconv have the iconvlist function.

package iconv

//#cgo LDFLAGS: -liconv -L/opt/local/lib
//#include <stdlib.h>
//#include <string.h>
//#include <iconv.h>
//#include <errno.h>
//iconv_t iconv_open_error = (iconv_t)-1;
//size_t iconv_error = (size_t)-1;
//
//typedef struct nameList nameList;
//struct nameList {
//	int n;
//	char **names;
//	nameList *next;
//};
//
//int
//addNames(unsigned int n, const char *const *names, void *data) {
//	// we can't call back to Go because of the stack size issue,
//	// so copy all the names.
//	nameList *hd, *e;
//	int i;
//
//	hd = data;
//	e = malloc(sizeof(nameList));
//	e->n = n;
//	e->names = malloc(sizeof(char*) * n);
//	for(i = 0; i < n; i++){
//		e->names[i] = strdup(names[i]);
//	}
//	e->next = hd->next;
//	hd->next = e;
//	return 0;
//}
//
//nameList *
//listNames(void) {
//	nameList hd;
//	hd.next = 0;
//	iconvlist(addNames, &hd);
//	return hd.next;
//}
import "C"

import (
	"sync"
)

var getAliasesOnce sync.Once
var allAliases = map[string][]string{}

func aliases() map[string][]string {
	getAliasesOnce.Do(getAliases)
}

func getAliases() {
	var next *C.nameList
	for p := C.listNames(); p != nil; p = next {
		next = p.next
		aliases := make([]string, p.n)
		pnames := (*[1e9]*C.char)(unsafe.Pointer(p.names))
		for i := range aliases {
			aliases[i] = C.GoString(pnames[i])
			C.free(unsafe.Pointer(pnames[i]))
		}
		C.free(unsafe.Pointer(p.names))
		C.free(unsafe.Pointer(p))
		for _, alias := range aliases {
			allAliases[alias] = aliases
		}
	}
}
