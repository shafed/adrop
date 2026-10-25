// Command cshared exports the adrop core as a C shared library for dart:ffi:
//
//	go build -buildmode=c-shared -o libadrop_core.so ./core/cshared
//
// Every call returns a JSON object {"ok":…,"result":…,"error":…} as a C string
// that the caller releases with adrop_free.
package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"encoding/json"
	"errors"
	"sync"
	"unsafe"

	"github.com/shafed/adrop/core"
)

var (
	mu   sync.Mutex
	node *core.Node
)

type reply struct {
	OK     bool   `json:"ok"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

func answer(result string, err error) *C.char {
	r := reply{OK: err == nil, Result: result}
	if err != nil {
		r.Error = err.Error()
	}
	raw, _ := json.Marshal(r)
	return C.CString(string(raw))
}

func started() (*core.Node, error) {
	mu.Lock()
	defer mu.Unlock()
	if node == nil {
		return nil, errors.New("core is not started")
	}
	return node, nil
}

//export adrop_start
func adrop_start(dataDir, name *C.char) *C.char {
	n, err := core.Start(C.GoString(dataDir), C.GoString(name))
	if err != nil {
		return answer("", err)
	}
	mu.Lock()
	node = n
	mu.Unlock()
	return answer(n.Fingerprint(), nil)
}

//export adrop_pair
func adrop_pair(uri *C.char) *C.char {
	n, err := started()
	if err != nil {
		return answer("", err)
	}
	return answer(n.Pair(C.GoString(uri)))
}

//export adrop_peers
func adrop_peers() *C.char {
	n, err := started()
	if err != nil {
		return answer("", err)
	}
	return answer(n.Peers(), nil)
}

//export adrop_hello
func adrop_hello(peer *C.char) *C.char {
	n, err := started()
	if err != nil {
		return answer("", err)
	}
	return answer(n.Hello(C.GoString(peer)))
}

//export adrop_free
func adrop_free(p *C.char) { C.free(unsafe.Pointer(p)) }

func main() {}
