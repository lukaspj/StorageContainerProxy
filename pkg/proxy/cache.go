package proxy

import (
	"bytes"
	"crypto/tls"
	"errors"
	"log"
	"net/http"
	"net/url"
)

func CheckUrlMD5(target *url.URL) (string, error) {
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}
	resp, err := client.Head(target.String())
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	contentMd5 := resp.Header["Content-Md5"]
	if len(contentMd5) != 1 {
		return "", errors.New("no md5 present")
	}
	return contentMd5[0], nil
}

type CachedResponseWriter struct {
	StatusCode int
	header     http.Header
	Buffer     bytes.Buffer
}

func NewCachedResponseWriter() *CachedResponseWriter {
	return &CachedResponseWriter{
		StatusCode: http.StatusOK,
		header:     make(http.Header),
		Buffer:     bytes.Buffer{},
	}
}

func (srrw *CachedResponseWriter) Header() http.Header {
	return srrw.header
}

func (srrw *CachedResponseWriter) Write(bytes []byte) (int, error) {
	return srrw.Buffer.Write(bytes)
}

func (srrw *CachedResponseWriter) WriteHeader(code int) {
	srrw.StatusCode = code
}

func (srrw CachedResponseWriter) WriteTo(res http.ResponseWriter) error {
	for k, v := range srrw.header {
		for _, s := range v {
			res.Header().Add(k, s)
		}
	}
	res.WriteHeader(srrw.StatusCode)
	_, err := res.Write(srrw.Buffer.Bytes())
	return err
}

type CachedResponse struct {
	md5   string
	value *CachedResponseWriter
}

type ResponseCache struct {
	cache map[string]map[string]*CachedResponse
}

func NewMd5ResponseCache() *ResponseCache {
	return &ResponseCache{
		cache: make(map[string]map[string]*CachedResponse),
	}
}

func (c *ResponseCache) get(req *http.Request) *CachedResponseWriter {
	if req.Method != http.MethodGet {
		return nil
	}

	if c.cache[req.Method] == nil {
		c.cache[req.Method] = make(map[string]*CachedResponse)
		return nil
	}
	r := c.cache[req.Method][req.URL.String()]
	log.Printf("[INFO] ccache: %v", c.cache)
	log.Printf("[INFO] ccache for %s: %v", req.URL.String(), c.cache[req.Method][req.URL.String()])
	if r == nil {
		return nil
	}

	urlMd5, err := CheckUrlMD5(req.URL)
	log.Printf("[INFO] md5 for: %s is %s\n", req.URL.String(), urlMd5)
	if err != nil {
		return nil
	}

	if r.md5 != urlMd5 {
		c.cache[req.Method][req.URL.String()] = nil
		return nil
	}

	return r.value
}

func (c *ResponseCache) put(req *http.Request, w *CachedResponseWriter) {
	if c.cache[req.Method] == nil {
		c.cache[req.Method] = make(map[string]*CachedResponse)
	}

	contentMd5 := w.Header()["Content-Md5"]
	log.Printf("[INFO] response headers are: %v\n", w.Header())
	log.Printf("[INFO] found md5 for: %s is %s\n", req.URL.String(), contentMd5)
	if len(contentMd5) != 1 {
		log.Printf("[INFO] len was %d\n", len(contentMd5))
		return
	}
	r := &CachedResponse{
		md5:   contentMd5[0],
		value: w,
	}
	c.cache[req.Method][req.URL.String()] = r
}
