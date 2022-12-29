/*
 * Licensed to the Apache Software Foundation (ASF) under one or more
 * contributor license agreements.  See the NOTICE file distributed with
 * this work for additional information regarding copyright ownership.
 * The ASF licenses this file to You under the Apache License, Version 2.0
 * (the "License"); you may not use this file except in compliance with
 * the License.  You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package http

/*
// ref https://github.com/golang/go/issues/25832

#cgo linux LDFLAGS: -Wl,-unresolved-symbols=ignore-all
#cgo darwin LDFLAGS: -Wl,-undefined,dynamic_lookup

#include <stdlib.h>
#include <string.h>

#include "api.h"

*/
import "C"
import (
	"reflect"
	"runtime"
	"strings"
	"unsafe"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"mosn.io/envoy-go-extension/pkg/api"
)

const (
	ValueRouteName = 1
)

type HttpCAPI interface {
	HttpContinue(r unsafe.Pointer, status uint64)
	HttpSendLocalReply(r unsafe.Pointer, responseCode int, bodyText string, headers map[string]string, grpcStatus int64, details string)

	// experience api, memory unsafe
	HttpGetHeader(r unsafe.Pointer, key *string, value *string)
	HttpCopyHeaders(r unsafe.Pointer, num uint64, bytes uint64) map[string][]string
	HttpSetHeader(r unsafe.Pointer, key *string, value *string, add bool)
	HttpRemoveHeader(r unsafe.Pointer, key *string)

	HttpGetBuffer(r unsafe.Pointer, bufferPtr uint64, value *string, length uint64)
	HttpSetBufferHelper(r unsafe.Pointer, bufferPtr uint64, value string, action api.BufferAction)

	HttpCopyTrailers(r unsafe.Pointer, num uint64, bytes uint64) map[string][]string
	HttpSetTrailer(r unsafe.Pointer, key *string, value *string)

	HttpGetRouteName(r unsafe.Pointer) string

	HttpGetDynamicMetadata(r *httpRequest, filterName string) map[string]interface{}
	HttpSetDynamicMetadata(r unsafe.Pointer, filterName string, key string, value interface{})

	HttpFinalize(r unsafe.Pointer, reason int)
}

type httpCApiImpl struct{}

// Only CAPIOK is expected, otherwise, panic here.
func handleCApiStatus(status C.int) {
	switch status {
	case C.CAPIOK:
		return
	case C.CAPIFilterIsGone:
		panic(ErrRequestFinished)
	case C.CAPIFilterIsDestroy:
		panic(ErrFilterDestroyed)
	case C.CAPINotInGo:
		panic(ErrNotInGo)
	case C.CAPIInvalidPhase:
		panic(ErrInvalidPhase)
	}
}

func (c *httpCApiImpl) HttpContinue(r unsafe.Pointer, status uint64) {
	res := C.moeHttpContinue(r, C.int(status))
	handleCApiStatus(res)
}

func (c *httpCApiImpl) HttpSendLocalReply(r unsafe.Pointer, response_code int, body_text string, headers map[string]string, grpc_status int64, details string) {
	hLen := len(headers)
	strs := make([]string, 0, hLen)
	for k, v := range headers {
		strs = append(strs, k, v)
	}
	res := C.moeHttpSendLocalReply(r, C.int(response_code), unsafe.Pointer(&body_text), unsafe.Pointer(&strs), C.longlong(grpc_status), unsafe.Pointer(&details))
	handleCApiStatus(res)
}

func (c *httpCApiImpl) HttpGetHeader(r unsafe.Pointer, key *string, value *string) {
	res := C.moeHttpGetHeader(r, unsafe.Pointer(key), unsafe.Pointer(value))
	handleCApiStatus(res)
}

func (c *httpCApiImpl) HttpCopyHeaders(r unsafe.Pointer, num uint64, bytes uint64) map[string][]string {
	// TODO: use a memory pool for better performance,
	// since these go strings in strs, will be copied into the following map.
	strs := make([]string, num*2)
	// but, this buffer can not be reused safely,
	// since strings may refer to this buffer as string data, and string is const in go.
	// we have to make sure the all strings is not using before reusing,
	// but strings may be alive beyond the request life.
	buf := make([]byte, bytes)
	sHeader := (*reflect.SliceHeader)(unsafe.Pointer(&strs))
	bHeader := (*reflect.SliceHeader)(unsafe.Pointer(&buf))

	res := C.moeHttpCopyHeaders(r, unsafe.Pointer(sHeader.Data), unsafe.Pointer(bHeader.Data))
	handleCApiStatus(res)

	m := make(map[string][]string, num)
	for i := uint64(0); i < num*2; i += 2 {
		key := strs[i]
		value := strs[i+1]
		// fmt.Printf("value of %s: %s\n", key, value)

		if v, found := m[key]; !found {
			m[key] = []string{value}
		} else {
			m[key] = append(v, value)
		}
	}
	runtime.KeepAlive(buf)
	return m
}

func (c *httpCApiImpl) HttpSetHeader(r unsafe.Pointer, key *string, value *string, add bool) {
	var act C.headerAction
	if add {
		act = C.HeaderAdd
	} else {
		act = C.HeaderSet
	}
	res := C.moeHttpSetHeaderHelper(r, unsafe.Pointer(key), unsafe.Pointer(value), act)
	handleCApiStatus(res)
}

func (c *httpCApiImpl) HttpRemoveHeader(r unsafe.Pointer, key *string) {
	res := C.moeHttpRemoveHeader(r, unsafe.Pointer(key))
	handleCApiStatus(res)
}

func (c *httpCApiImpl) HttpGetBuffer(r unsafe.Pointer, bufferPtr uint64, value *string, length uint64) {
	buf := make([]byte, length)
	bHeader := (*reflect.SliceHeader)(unsafe.Pointer(&buf))
	sHeader := (*reflect.StringHeader)(unsafe.Pointer(value))
	sHeader.Data = bHeader.Data
	sHeader.Len = int(length)
	res := C.moeHttpGetBuffer(r, C.ulonglong(bufferPtr), unsafe.Pointer(bHeader.Data))
	handleCApiStatus(res)
}

func (c *httpCApiImpl) HttpSetBufferHelper(r unsafe.Pointer, bufferPtr uint64, value string, action api.BufferAction) {
	sHeader := (*reflect.StringHeader)(unsafe.Pointer(&value))
	var act C.bufferAction
	switch action {
	case api.SetBuffer:
		act = C.Set
	case api.AppendBuffer:
		act = C.Append
	case api.PrependBuffer:
		act = C.Prepend
	}
	res := C.moeHttpSetBufferHelper(r, C.ulonglong(bufferPtr), unsafe.Pointer(sHeader.Data), C.int(sHeader.Len), act)
	handleCApiStatus(res)
}

func (c *httpCApiImpl) HttpCopyTrailers(r unsafe.Pointer, num uint64, bytes uint64) map[string][]string {
	// TODO: use a memory pool for better performance,
	// but, should be very careful, since string is const in go,
	// and we have to make sure the strings is not using before reusing,
	// strings may be alive beyond the request life.
	strs := make([]string, num*2)
	buf := make([]byte, bytes)
	sHeader := (*reflect.SliceHeader)(unsafe.Pointer(&strs))
	bHeader := (*reflect.SliceHeader)(unsafe.Pointer(&buf))

	res := C.moeHttpCopyTrailers(r, unsafe.Pointer(sHeader.Data), unsafe.Pointer(bHeader.Data))
	handleCApiStatus(res)

	m := make(map[string][]string, num)
	for i := uint64(0); i < num*2; i += 2 {
		key := strs[i]
		value := strs[i+1]
		if v, found := m[key]; !found {
			m[key] = []string{value}
		} else {
			m[key] = append(v, value)
		}
	}
	return m
}

func (c *httpCApiImpl) HttpSetTrailer(r unsafe.Pointer, key *string, value *string) {
	res := C.moeHttpSetTrailer(r, unsafe.Pointer(key), unsafe.Pointer(value))
	handleCApiStatus(res)
}

func (c *httpCApiImpl) HttpGetRouteName(r unsafe.Pointer) string {
	var value string
	res := C.moeHttpGetStringValue(r, ValueRouteName, unsafe.Pointer(&value))
	handleCApiStatus(res)
	// copy the memory from c to Go.
	return strings.Clone(value)
}

func (c *httpCApiImpl) HttpFinalize(r unsafe.Pointer, reason int) {
	C.moeHttpFinalize(r, C.int(reason))
}

func (c *httpCApiImpl) HttpGetDynamicMetadata(r *httpRequest, filterName string) map[string]interface{} {
	var buf []byte
	r.sema.Add(1)
	res := C.moeHttpGetDynamicMetadata(unsafe.Pointer(r.req), unsafe.Pointer(&filterName), unsafe.Pointer(&buf))
	if res == C.CAPIYield {
		// means C post a callback to the Envoy worker thread, waitting the C callback
		r.sema.Wait()
	} else {
		r.sema.Done()
		handleCApiStatus(res)
	}
	// means not found
	if len(buf) == 0 {
		return map[string]interface{}{}
	}
	// copy the memory from c to Go.
	var meta structpb.Struct
	proto.Unmarshal(buf, &meta)
	return meta.AsMap()
}

func (c *httpCApiImpl) HttpSetDynamicMetadata(r unsafe.Pointer, filterName string, key string, value interface{}) {
	v, err := structpb.NewValue(value)
	if err != nil {
		panic(err)
	}
	buf, err := proto.Marshal(v)
	if err != nil {
		panic(err)
	}
	res := C.moeHttpSetDynamicMetadata(r, unsafe.Pointer(&filterName), unsafe.Pointer(&key), unsafe.Pointer(&buf))
	handleCApiStatus(res)
}

var cAPI HttpCAPI = &httpCApiImpl{}

// SetHttpCAPI for mock cAPI
func SetHttpCAPI(api HttpCAPI) {
	cAPI = api
}
