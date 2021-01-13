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
	contentMd5 := resp.Header["Content-Md5"]
	resp.Body.Close()
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

func (c *ResponseCache) get(method string, target *url.URL) *CachedResponseWriter {
	if method != http.MethodGet {
		return nil
	}

	if c.cache[method] == nil {
		c.cache[method] = make(map[string]*CachedResponse)
		return nil
	}
	r := c.cache[method][target.Path]
	log.Printf("[INFO] ccache: %v", c.cache)
	log.Printf("[INFO] ccache for %s: %v", target.String(), r)
	if r == nil {
		return nil
	}

	urlMd5, err := CheckUrlMD5(target)
	log.Printf("[INFO] ResponseCache::get md5 for: %s is %s\n", target.String(), urlMd5)
	if err != nil {
		log.Printf("[ERROR] ResponseCache::get %v\n", err)
		return nil
	}

	if r.md5 != urlMd5 {
		c.cache[method][target.Path] = nil
		log.Printf("[WARN] ResponseCache::get md5 mismatch: %s != %s -- updating\n", r.md5, urlMd5)
		return nil
	}

	return r.value
}

func (c *ResponseCache) put(method string, target *url.URL, w *CachedResponseWriter) {
	if c.cache[method] == nil {
		c.cache[method] = make(map[string]*CachedResponse)
	}

	contentMd5 := w.Header()["Content-Md5"]
	log.Printf("[INFO] response headers are: %v\n", w.Header())
	log.Printf("[INFO] found md5 for: %s is %s\n", target.Path, contentMd5)
	if len(contentMd5) != 1 {
		log.Printf("[INFO] len was %d\n", len(contentMd5))
		return
	}
	r := &CachedResponse{
		md5:   contentMd5[0],
		value: w,
	}
	c.cache[method][target.Path] = r
}
