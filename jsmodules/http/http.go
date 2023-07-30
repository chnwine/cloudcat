// Package http the http JS implementation
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	urlpkg "net/url"
	"strings"

	"github.com/dop251/goja"
	"github.com/shiroyk/cloudcat/core"
	"github.com/shiroyk/cloudcat/core/js"
	"github.com/shiroyk/cloudcat/plugin/jsmodule"
	"github.com/spf13/cast"
	"golang.org/x/net/http/httpguts"
)

// Module js module
type Module struct{}

// Exports returns module instance
func (*Module) Exports() any {
	return &Http{cloudcat.MustResolve[cloudcat.Fetch]()}
}

func init() {
	jsmodule.Register("http", new(Module))
	jsmodule.Register("fetch", new(fetch))
	jsmodule.Register("FormData", new(FormDataConstructor))
	jsmodule.Register("URLSearchParams", new(URLSearchParamsConstructor))
	jsmodule.Register("AbortSignal", new(AbortSignalConstructor))
}

type fetch struct{}

func (*fetch) Exports() any {
	f := cloudcat.MustResolve[cloudcat.Fetch]()
	return func(call goja.FunctionCall, vm *goja.Runtime) (ret goja.Value) {
		return doRequest(f, http.MethodGet, call, vm)
	}
}

// Http module for fetching resources (including across the network).
type Http struct { //nolint
	fetch cloudcat.Fetch
}

// Get Make a HTTP GET request.
func (h *Http) Get(call goja.FunctionCall, vm *goja.Runtime) (ret goja.Value) {
	return doRequest(h.fetch, http.MethodGet, call, vm)
}

// Post Make a HTTP POST.
// Send POST with multipart:
// http.post(url, { body: new FormData({'bytes': new Uint8Array([0]).buffer}) })
// Send POST with x-www-form-urlencoded:
// http.post(url, { body: new URLSearchParams({'key': 'foo', 'value': 'bar'}) })
// Send POST with json:
// http.post(url, { body: {'key': 'foo'} })
func (h *Http) Post(call goja.FunctionCall, vm *goja.Runtime) (ret goja.Value) {
	return doRequest(h.fetch, http.MethodPost, call, vm)
}

// Put Make a HTTP PUT request.
func (h *Http) Put(call goja.FunctionCall, vm *goja.Runtime) (ret goja.Value) {
	return doRequest(h.fetch, http.MethodPut, call, vm)
}

// Delete Make a HTTP DELETE request.
func (h *Http) Delete(call goja.FunctionCall, vm *goja.Runtime) (ret goja.Value) {
	return doRequest(h.fetch, http.MethodDelete, call, vm)
}

// Head Make a HTTP HEAD request.
func (h *Http) Head(call goja.FunctionCall, vm *goja.Runtime) (ret goja.Value) {
	return doRequest(h.fetch, http.MethodHead, call, vm)
}

func doRequest(
	fetch cloudcat.Fetch,
	method string,
	call goja.FunctionCall,
	vm *goja.Runtime,
) goja.Value {
	url := call.Argument(0).String()
	opt := call.Argument(1)
	var body io.Reader
	var headers = make(map[string]string)
	var proxy *urlpkg.URL
	var signal *AbortSignal
	var err error

	if opt != nil && !goja.IsUndefined(opt) {
		options, assert := opt.Export().(map[string]any)
		if !assert {
			js.Throw(vm, errors.New("request options must be an object"))
		}
		if v, ok := options["method"]; ok {
			method, err = cast.ToStringE(v)
			if err != nil {
				js.Throw(vm, err)
			}
			method = strings.ToUpper(method)
			if !validMethod(method) {
				js.Throw(vm, fmt.Errorf("request options method %v is not valid HTTP method", method))
			}
		}
		if v, ok := options["url"]; url == "" && ok {
			url, err = cast.ToStringE(v)
			if err != nil {
				js.Throw(vm, err)
			}
		}
		if v, ok := options["headers"]; ok {
			headers, err = cast.ToStringMapStringE(v)
			if err != nil {
				js.Throw(vm, errors.New("request options headers is not valid object"))
			}
		}
		if v, ok := options["body"]; ok {
			body, err = handleBody(v, headers)
			if err != nil {
				js.Throw(vm, err)
			}
		}
		if v, ok := options["cache"]; ok {
			str, err := cast.ToStringE(v)
			if err != nil {
				js.Throw(vm, err)
			}
			headers["Cache-Control"] = str
			headers["Pragma"] = str
		}
		if v, ok := options["proxy"]; ok {
			str, err := cast.ToStringE(v)
			if err != nil {
				js.Throw(vm, err)
			}
			proxy, err = urlpkg.Parse(str)
			if err != nil {
				js.Throw(vm, err)
			}
		}
		if v, ok := options["signal"]; ok {
			signal, ok = v.(*AbortSignal)
			if !ok {
				js.Throw(vm, errors.New("request options signal is not Signal object"))
			}
		}
	}

	var parent context.Context
	if signal != nil {
		parent = signal.ctx
		defer signal.timeout()
	} else {
		parent = js.VMContext(vm)
	}

	ctx := cloudcat.WithProxyURL(parent, proxy)

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		js.Throw(vm, err)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	res, err := fetch.Do(req)
	if err != nil {
		js.Throw(vm, err)
	}

	return NewResponse(vm, res)
}

func validMethod(method string) bool {
	/*
	     Method         = "OPTIONS"                ; Section 9.2
	                    | "GET"                    ; Section 9.3
	                    | "HEAD"                   ; Section 9.4
	                    | "POST"                   ; Section 9.5
	                    | "PUT"                    ; Section 9.6
	                    | "DELETE"                 ; Section 9.7
	                    | "TRACE"                  ; Section 9.8
	                    | "CONNECT"                ; Section 9.9
	                    | extension-method
	   extension-method = token
	     token          = 1*<any CHAR except CTLs or separators>
	*/
	return len(method) > 0 && strings.IndexFunc(method, func(r rune) bool {
		return !httpguts.IsTokenRune(r)
	}) == -1
}

// handleBody process the send request body and set the content-type
func handleBody(body any, headers map[string]string) (io.Reader, error) {
	switch data := body.(type) {
	case FormData:
		buf := new(bytes.Buffer)
		mpw := multipart.NewWriter(buf)
		for k, v := range data.data {
			for _, ve := range v {
				if f, ok := ve.(FileData); ok {
					// Creates a new form-data header with the provided field name and file name.
					fw, err := mpw.CreateFormFile(k, f.Filename)
					if err != nil {
						return nil, err
					}
					// Write bytes to the part
					if _, err := fw.Write(f.Data); err != nil {
						return nil, err
					}
				} else {
					// Write string value
					if err := mpw.WriteField(k, fmt.Sprintf("%v", v)); err != nil {
						return nil, err
					}
				}
			}
		}
		headers["Content-Type"] = mpw.FormDataContentType()
		if err := mpw.Close(); err != nil {
			return nil, err
		}
		return buf, nil
	case URLSearchParams:
		headers["Content-Type"] = "application/x-www-form-url"
		return strings.NewReader(data.encode()), nil
	case string:
		return strings.NewReader(data), nil
	case goja.ArrayBuffer:
		return bytes.NewReader(data.Bytes()), nil
	case []byte:
		return bytes.NewReader(data), nil
	case map[string]any:
		headers["Content-Type"] = "application/json"
		marshal, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		return bytes.NewReader(marshal), nil
	case nil:
		return http.NoBody, nil
	default:
		return nil, fmt.Errorf("unsupported request body type %T", body)
	}
}
