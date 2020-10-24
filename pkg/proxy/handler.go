package proxy

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/go-chi/chi"
)

type Config struct {
	AzureStorageAccount   string
	AzureStorageContainer string
	BaseDomain            string
}

type StorageContainerProxyHandler struct {
	AzureStorageAccount   string
	AzureStorageContainer string
	BaseDomain            string
}

func NewHandler(config *Config) StorageContainerProxyHandler {
	return StorageContainerProxyHandler{
		AzureStorageAccount:   config.AzureStorageAccount,
		AzureStorageContainer: config.AzureStorageContainer,
		BaseDomain:            config.BaseDomain,
	}
}

func NewStorageContainerReverseProxy(target *url.URL) *httputil.ReverseProxy {
	targetQuery := target.RawQuery
	director := func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path, req.URL.RawPath = joinURLPath(target, req.URL)
		if targetQuery == "" || req.URL.RawQuery == "" {
			req.URL.RawQuery = targetQuery + req.URL.RawQuery
		} else {
			req.URL.RawQuery = targetQuery + "&" + req.URL.RawQuery
		}
		if _, ok := req.Header["User-Agent"]; !ok {
			// explicitly disable User-Agent so it's not set to default value
			req.Header.Set("User-Agent", "")
		}
		req.Host = target.Host
		log.Printf("Proxy request to: %s\n", req.URL)
	}
	return &httputil.ReverseProxy{Director: director}
}

func (scp *StorageContainerProxyHandler) Listen() {
	port := 3000

	r := chi.NewRouter()

	// r.Use(SubdomainAsSubpath(scp.BaseDomain))
	r.Use(TryIndexOnNotFound)

	r.Handle("/*", NewStorageContainerReverseProxy(&url.URL{
		Scheme: "https",
		Host:   fmt.Sprintf("%s.blob.core.windows.net", scp.AzureStorageAccount),
		Path:   fmt.Sprintf("/%s", scp.AzureStorageContainer),
	}))

	err := http.ListenAndServe(fmt.Sprintf(":%d", port), r)
	if err != nil {
		log.Fatal(fmt.Sprintf("%e", err))
	}
}

func SubdomainAsSubpath(domain string) func(http.Handler) http.Handler {
	domainDotCount := strings.Count(domain, ".")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
			host := req.Host[:strings.Index(req.Host, ":")]
			if !strings.HasSuffix(host, domain) {
				log.Printf("ERROR: %s did not match base domain %s", host, domain)
				res.WriteHeader(500)
				return
			}
			hostDotCount := strings.Count(host, ".")
			if hostDotCount == domainDotCount {
				// Default path
				req.URL.Path = "/default" + req.URL.Path
			} else if hostDotCount == domainDotCount+1 {
				// Sub-path
				req.URL.Path = strings.TrimSuffix(host, domain)
			} else {
				// Too many subdomains
				log.Printf("ERROR: %s had too many subdomains compared to %s", host, domain)
				res.WriteHeader(500)
				return
			}
			next.ServeHTTP(res, req)
		})
	}
}

type StatusRecorderResponseWriter struct {
	StatusCode int
	header     http.Header
	Buffer     bytes.Buffer
}

func NewStatusRecorderResponseWriter() *StatusRecorderResponseWriter {
	return &StatusRecorderResponseWriter{
		StatusCode: http.StatusOK,
		header:     make(http.Header),
		Buffer:     bytes.Buffer{},
	}
}

func (srrw *StatusRecorderResponseWriter) Header() http.Header {
	return srrw.header
}

func (srrw *StatusRecorderResponseWriter) Write(bytes []byte) (int, error) {
	return srrw.Buffer.Write(bytes)
}

func (srrw *StatusRecorderResponseWriter) WriteHeader(code int) {
	srrw.StatusCode = code
}

func (srrw StatusRecorderResponseWriter) WriteTo(res http.ResponseWriter) error {
	res.WriteHeader(srrw.StatusCode)
	for k, v := range srrw.header {
		for _, s := range v {
			res.Header().Add(k, s)
		}
	}
	_, err := res.Write(srrw.Buffer.Bytes())
	return err
}

func TryIndexOnNotFound(next http.Handler) http.Handler {
	return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		statusWriter := NewStatusRecorderResponseWriter()
		next.ServeHTTP(statusWriter, req)
		if !strings.HasSuffix(req.URL.Path, "/index.html") && statusWriter.StatusCode == 404 {
			statusWriter = NewStatusRecorderResponseWriter()
			log.Printf("%s was not found, trying index.html instead\n", req.URL)
			req.URL.Path = req.URL.Path[:strings.LastIndex(req.URL.Path, "/")-1] + "/index.html"
			next.ServeHTTP(statusWriter, req)
		}
		statusWriter.WriteTo(res)
	})
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

func joinURLPath(a, b *url.URL) (path, rawpath string) {
	if a.RawPath == "" && b.RawPath == "" {
		return singleJoiningSlash(a.Path, b.Path), ""
	}
	// Same as singleJoiningSlash, but uses EscapedPath to determine
	// whether a slash should be added
	apath := a.EscapedPath()
	bpath := b.EscapedPath()

	aslash := strings.HasSuffix(apath, "/")
	bslash := strings.HasPrefix(bpath, "/")

	switch {
	case aslash && bslash:
		return a.Path + b.Path[1:], apath + bpath[1:]
	case !aslash && !bslash:
		return a.Path + "/" + b.Path, apath + "/" + bpath
	}
	return a.Path + b.Path, apath + bpath
}

// https://bryrupteaterfrontend.blob.core.windows.net/bt-administration/1-es2015.549f007b582c945621d8.js
