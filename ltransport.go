package main

import (
	"bufio"
	"bytes"
	"net/http"
	"net/http/httputil"
	"strconv"
	"time"

	"github.com/pquerna/cachecontrol"
)

const (
	stale = iota
	fresh
	transparent
	// XFromCache is the header added to responses that are returned from the cache
	XFromCache = "X-From-Cache"
)

// A Cache interface is used by the Transport to store and retrieve responses.
type Cache interface {
	// Get returns the []byte representation of a cached response and a bool
	// set to true if the value isn't empty
	Get(key string) (responseBytes []byte, ok bool)
	// Set stores the []byte representation of a response against a key
	Set(key string, responseBytes []byte)
	// Delete removes the value associated with the key
	Delete(key string)
}

type Heuristic interface {
	PreCaching(response *http.Response)
	PostRequest(response *http.Response)
	Cacheable(r *http.Request) bool
	CacheKey(r *http.Request) string
}

type Transport struct {
	// The RoundTripper interface actually used to make requests
	// If nil, http.DefaultTransport is used
	Transport  http.RoundTripper
	Cache      Cache
	THeuristic Heuristic
	// If true, responses returned from the cache will be given an extra header, X-From-Cache
	MarkCachedResponses bool
}

type HueristicStruct struct {
	CacheForAmount time.Duration
	requestCount   int
}

func (h *HueristicStruct) PostRequest(response *http.Response) {
	response.Header.Set("expires", time.Now().Add(h.CacheForAmount * time.Second).Format(http.TimeFormat))
	response.Header.Set("cache-control", "public")
	response.Header.Set("x-request-count", strconv.Itoa(h.requestCount))
	h.requestCount++
}

func (*HueristicStruct) Cacheable(r *http.Request) bool {
	return true
}

func (*HueristicStruct) CacheKey(r *http.Request) string {
	return r.Method + " " + r.URL.String()
}

func (*HueristicStruct) PreCaching(response *http.Response) {
	// panic("implement me")
	return
}

func DefaultHeuristic() Heuristic {
	return &HueristicStruct{60 * 60 * 24 * 365 * 10, 0}
}
func (t *Transport) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	cacheKey := t.THeuristic.CacheKey(req)
	cacheable := t.THeuristic.Cacheable(req)
	var cachedResp *http.Response
	if cacheable {
		cachedResp, err = CachedResponse(t.Cache, req, cacheKey)
	} else {
		// Need to invalidate an existing value
		t.Cache.Delete(cacheKey)
	}
	transport := t.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if cacheable && cachedResp != nil && err == nil {
		if t.MarkCachedResponses {
			cachedResp.Header.Set(XFromCache, "1")
		}
		reasons, expires, err := cachecontrol.CachableResponse(req, cachedResp, cachecontrol.Options{})
		if err != nil {
			return nil, err
		}
		if len(reasons) == 0 && time.Now().Before(expires) {
			return cachedResp, nil
		}
	}
	resp, err = transport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return resp, err
	}
	t.THeuristic.PostRequest(resp)
	resp.Header.Set("x-source-request", req.URL.String())
	reasons, expires, err := cachecontrol.CachableResponse(req, resp, cachecontrol.Options{})
	// fmt.Println(reasons, expires,time.Now().Before(expires), err!=nil, cacheable)
	if len(reasons) == 0 && err == nil && cacheable && time.Now().Before(expires) {
		t.THeuristic.PreCaching(resp)
		respBytes, err := httputil.DumpResponse(resp, true)
		if err == nil {
			t.Cache.Set(cacheKey, respBytes)
		}
	} else {
		t.Cache.Delete(cacheKey)
	}
	return
}

// CachedResponse returns the cached http.Response for req if present, and nil
// otherwise.
func CachedResponse(c Cache, req *http.Request, cacheKey string) (resp *http.Response, err error) {
	cachedVal, ok := c.Get(cacheKey)
	if !ok {
		return
	}

	b := bytes.NewBuffer(cachedVal)
	return http.ReadResponse(bufio.NewReader(b), req)
}

func NewHuersticTransport(c Cache) *Transport {
	return &Transport{Cache: c, MarkCachedResponses: true, THeuristic: DefaultHeuristic()}
}
