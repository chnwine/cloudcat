package fetch

import (
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/andybalholm/brotli"
)

func TestCharsetFromHeaders(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=iso-8859-9")
		_, _ = fmt.Fprint(w, "G\xfcltekin")
	}))
	defer ts.Close()

	res, _ := newFetcherDefault().Get(ts.URL, nil)

	if res.String() != "Gültekin" {
		t.Fatal(res.String())
	}
}

func TestCharsetFromBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "G\xfcltekin")
	}))
	defer ts.Close()

	res, _ := newFetcherDefault().Post(ts.URL, nil, nil)

	if res.String() != "Gültekin" {
		t.Fatal(res.String())
	}
}

func TestCharsetProvidedWithRequest(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "G\xfcltekin")
	}))
	defer ts.Close()

	req, _ := NewRequest("GET", ts.URL, nil, nil)
	req.Encoding = "windows-1254"
	res, _ := newFetcherDefault().DoRequest(req)

	if res.String() != "Gültekin" {
		t.Fatal(res.String())
	}
}

func TestRetry(t *testing.T) {
	var times atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if times.Load() < DefaultRetryTimes {
			times.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte{226})
		}
	}))
	defer ts.Close()

	fetch := newFetcherDefault()

	for i, s := range []string{"Status code retry", "Other error retry"} {
		t.Run(s, func(t *testing.T) {
			var res *Response
			var err error
			if i > 0 {
				res, err = fetch.Get(ts.URL, map[string]string{"Location": "\x00"})
			} else {
				res, err = fetch.Head(ts.URL, nil)
			}

			if err != nil {
				if !strings.Contains(err.Error(), "Location") {
					t.Fatal(err)
				}
			} else {
				if res.StatusCode != http.StatusOK {
					t.Fatalf("unexpected response status %v", res.StatusCode)
				}
			}
		})
	}
}

func TestCancel(t *testing.T) {
	fetch := newFetcherDefault()

	req, err := NewRequest(http.MethodGet, "", nil, nil)
	if err != nil {
		t.Error(err)
	}

	req.Cancel()

	_, err = fetch.DoRequest(req)
	if err != ErrRequestCancel {
		t.Fatal(err)
	}
}

func TestDecompress(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		encoding := r.Header.Get("Content-Encoding")
		w.Header().Set("Content-Encoding", encoding)
		w.Header().Set("Content-Type", "text/plain")

		var bodyWriter io.WriteCloser
		switch encoding {
		case "deflate":
			bodyWriter = zlib.NewWriter(w)
		case "gzip":
			bodyWriter = gzip.NewWriter(w)
		case "br":
			bodyWriter = brotli.NewWriter(w)
		}
		defer bodyWriter.Close()

		bytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}

		_, _ = bodyWriter.Write(bytes)
	}))
	defer ts.Close()

	testCases := []struct {
		compress, want string
	}{
		{"deflate", "test1"},
		{"gzip", "test2"},
		{"br", "test3"},
	}

	fetch := newFetcherDefault()

	for _, testCase := range testCases {
		t.Run(testCase.compress, func(t *testing.T) {
			res, err := fetch.Post(ts.URL, testCase.want, map[string]string{
				"Content-Encoding": testCase.compress,
			})
			if err != nil {
				t.Error(err)
			}

			if res.String() != testCase.want {
				t.Errorf("want %v, got %v", testCase.want, res.String())
			}
		})
	}
}

// newFetcherDefault creates new client with default options
func newFetcherDefault() Fetch {
	return NewFetcher(Options{
		MaxBodySize:    DefaultMaxBodySize,
		RetryTimes:     DefaultRetryTimes,
		RetryHTTPCodes: DefaultRetryHTTPCodes,
		Timeout:        DefaultTimeout,
	})
}