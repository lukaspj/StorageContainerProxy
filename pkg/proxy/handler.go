package proxy

import (
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/go-chi/cors"
)

type Config struct {
	AzureStorageAccount   string
	AzureStorageContainer string
	BaseDomain            string
	DefaultEnv            string
	UseSubdomains         bool
}

type StorageContainerProxyHandler struct {
	AzureStorageAccount   string
	AzureStorageContainer string
	BaseDomain            string
	DefaultEnv            string
	UseSubdomains         bool
	Target                *url.URL
}

func NewHandler(config *Config) StorageContainerProxyHandler {
	return StorageContainerProxyHandler{
		AzureStorageAccount:   config.AzureStorageAccount,
		AzureStorageContainer: config.AzureStorageContainer,
		BaseDomain:            config.BaseDomain,
		DefaultEnv:            config.DefaultEnv,
		UseSubdomains:         config.UseSubdomains,
		Target: &url.URL{
			Scheme: "https",
			Host:   fmt.Sprintf("%s.blob.core.windows.net", config.AzureStorageAccount),
			Path:   fmt.Sprintf("/%s", config.AzureStorageContainer),
		},
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
	return &httputil.ReverseProxy{
		Director: director,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

func (scp *StorageContainerProxyHandler) Listen() {
	port := 3000

	r := chi.NewRouter()

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{
			"http://localhost",
			"http://localhost:*",
			"http://127.0.0.1",
			fmt.Sprintf("https://%s", scp.BaseDomain),
			fmt.Sprintf("https://*.%s", scp.BaseDomain),
			fmt.Sprintf("%s://%s", scp.Target.Scheme, scp.Target.Host)},
		AllowedHeaders: []string{"*"},
	}))
	r.Use(middleware.Compress(5))
	if scp.UseSubdomains {
		r.Use(SubdomainAsSubpath(scp.BaseDomain, scp.DefaultEnv))
	} else {
		r.Use(TryDefaultEnvOnNotFound(scp.DefaultEnv))
	}
	r.Use(RedirectAssetsByExtension(scp.Target, []string{".jpg", ".png", ".jpeg", ".zip", ".js"}))
	r.Use(middleware.ThrottleBacklog(5, 20000, 30*time.Second))
	r.Use(TryIndexOnNotFound())
	r.Use(AddHtmlIfNoExtensionAndNotFound())
	r.Use(AddTrailingSlashIfNoExtensionAndNotFound(scp.Target))
	r.Use(Md5Cache(scp.Target))

	r.Handle("/*", NewStorageContainerReverseProxy(scp.Target))

	err := http.ListenAndServe(fmt.Sprintf(":%d", port), r)
	if err != nil {
		log.Fatal(fmt.Sprintf("%e", err))
	}
}

func GetUrlFromRequest(req *http.Request) *url.URL {
	scheme := "http"
	if req.TLS != nil {
		scheme = "https"
	}

	return &url.URL{
		Scheme: scheme,
		Host:   req.Host,
	}
}

func SubdomainAsSubpath(domain string, env string) func(http.Handler) http.Handler {
	domainDotCount := strings.Count(domain, ".")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
			host := req.Host
			if strings.Contains(host, ":") {
				host = host[:strings.Index(host, ":")]
			}
			if !strings.HasSuffix(host, domain) {
				log.Printf("ERROR: %s did not match base domain %s", host, domain)
				res.WriteHeader(500)
				return
			}
			hostDotCount := strings.Count(host, ".")
			req.URL.RawPath = ""
			if hostDotCount == domainDotCount {
				// Default path
				req.URL.Path = "/" + env + req.URL.Path
			} else if hostDotCount == domainDotCount+1 {
				// Sub-path
				req.URL.Path = "/" + strings.TrimSuffix(host, "."+domain) + req.URL.Path
				log.Printf("[INFO] updated url path to: %s, based on subdomain", req.URL.Path)
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

func CheckUrlExists(target *url.URL) (int, error) {
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}
	resp, err := client.Head(target.String())
	if err != nil {
		return -1, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

func AddHtmlIfNoExtensionAndNotFound() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
			w := NewCachedResponseWriter()

			next.ServeHTTP(w, req)

			if w.StatusCode == 404 && !strings.HasSuffix(req.URL.Path, "/") && filepath.Ext(req.URL.Path) == "" {
				req.URL.RawPath = ""
				req.URL.Path = req.URL.Path + ".html"
				next.ServeHTTP(res, req)
			} else {
				err := w.WriteTo(res)
				if err != nil {
					res.WriteHeader(500)
					log.Printf("[ERROR] %v\n", err)
				}
			}
		})
	}
}

func AddTrailingSlashIfNoExtensionAndNotFound(target *url.URL) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
			w := NewCachedResponseWriter()

			next.ServeHTTP(w, req)

			if w.StatusCode == 404 && !strings.HasSuffix(req.URL.Path, "/") && filepath.Ext(req.URL.Path) == "" {
				log.Printf("%s was not found, trying %s/index.html instead\n", req.URL.String(), req.URL.String())
				req.URL.RawPath = ""
				req.URL.Path = req.URL.Path + "/index.html"

				next.ServeHTTP(res, req)
			} else {
				err := w.WriteTo(res)
				if err != nil {
					res.WriteHeader(500)
					log.Printf("[ERROR] %v\n", err)
				}
			}
		})
	}
}

func TryDefaultEnvOnNotFound(defaultEnv string) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
			w := NewCachedResponseWriter()

			next.ServeHTTP(w, req)

			if w.StatusCode == 404 && !strings.HasSuffix(req.URL.Path, "/index.html") {
				newPath := "/" + defaultEnv + req.URL.Path
				log.Printf("%s was not found (path: %s), trying %s instead\n", req.URL.String(), req.URL.Path, newPath)
				req.URL.RawPath = ""
				req.URL.Path = newPath
				next.ServeHTTP(res, req)
			} else {
				err := w.WriteTo(res)
				if err != nil {
					res.WriteHeader(500)
					log.Printf("[ERROR] %v\n", err)
				}

				return
			}
		})
	}
}

func TryIndexOnNotFound() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
			w := NewCachedResponseWriter()

			next.ServeHTTP(w, req)

			if w.StatusCode == 404 && !strings.HasSuffix(req.URL.Path, "/index.html") {
				log.Printf("%s was not found (path: %s), trying index.html instead\n", req.URL.String(), req.URL.Path)
				req.URL.RawPath = ""
				req.URL.Path = req.URL.Path[:strings.LastIndex(req.URL.Path, "/")] + "/index.html"
				next.ServeHTTP(res, req)
			} else {
				err := w.WriteTo(res)
				if err != nil {
					res.WriteHeader(500)
					log.Printf("[ERROR] %v\n", err)
				}

				return
			}
		})
	}
}

func RedirectAssetsByExtension(target *url.URL, extensions []string) func(http.Handler) http.Handler {
	targetQuery := target.RawQuery
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
			ext := filepath.Ext(req.URL.Path)
			log.Printf("[INFO] extension is: %s\n", ext)
			for _, e := range extensions {
				if ext == e {
					redirectUrl := url.URL{}
					redirectUrl.Scheme = target.Scheme
					redirectUrl.Host = target.Host
					redirectUrl.Path, req.URL.RawPath = joinURLPath(target, req.URL)
					if targetQuery == "" || req.URL.RawQuery == "" {
						redirectUrl.RawQuery = targetQuery + req.URL.RawQuery
					} else {
						redirectUrl.RawQuery = targetQuery + "&" + req.URL.RawQuery
					}

					http.Redirect(res, req, redirectUrl.String(), 302)
					return
				}
			}

			next.ServeHTTP(res, req)
		})
	}
}

func Md5Cache(target *url.URL) func(next http.Handler) http.Handler {
	cache := NewMd5ResponseCache(10 * time.Second)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
			urlCopy := &url.URL{}
			*urlCopy = *target
			urlCopy.Path, urlCopy.RawPath = joinURLPath(urlCopy, req.URL)

			cachedRes := cache.get(req.Method, urlCopy)
			if cachedRes != nil {
				log.Printf("[INFO] found a cached version for %s\n", req.URL.String())
				cachedRes.WriteTo(res)
				return
			}

			log.Printf("[INFO] update cache for %s\n", req.URL.String())
			innerRes := NewCachedResponseWriter()
			next.ServeHTTP(innerRes, req)
			cache.put(req.Method, urlCopy, innerRes)
			innerRes.WriteTo(res)
		})
	}
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
