package router

import (
	"fmt"
	"net/http"
	"slices"
)

type MiddlewareFunc func(http.Handler) http.Handler

var validMethods = []string{
	http.MethodGet,
	http.MethodHead,
	http.MethodPost,
	http.MethodPut,
	http.MethodPatch,
	http.MethodDelete,
	http.MethodConnect,
	http.MethodOptions,
	http.MethodTrace,
}

type RouteConfig struct {
	Method      string
	Endpoint    string
	HandlerFunc http.HandlerFunc
}

func (rc RouteConfig) Valid() bool {
	return slices.Contains(validMethods, rc.Method)
}

type Router struct {
	routes      []RouteConfig
	middlewares []MiddlewareFunc
	NotFound    http.HandlerFunc
}

func NewRouter() *Router {
	return &Router{
		routes:      []RouteConfig{},
		middlewares: []MiddlewareFunc{},
		NotFound: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("not found"))
		},
	}
}

func (r *Router) Route(method, endpoint string, handler http.HandlerFunc) error {
	return r.RegisterRoutes(RouteConfig{
		Method:      method,
		Endpoint:    endpoint,
		HandlerFunc: handler,
	})
}

func (r *Router) RegisterRoutes(configs ...RouteConfig) error {
	for _, config := range configs {
		if !config.Valid() {
			return fmt.Errorf("invalid HTTP method: %s", config.Method)
		}
		r.routes = append(r.routes, config)
	}
	return nil
}

func (r *Router) Use(mw MiddlewareFunc) {
	r.middlewares = append(r.middlewares, mw)
}

func (r *Router) wrap(handler http.HandlerFunc) http.HandlerFunc {
	h := http.Handler(handler)
	for i := len(r.middlewares) - 1; i >= 0; i-- {
		h = r.middlewares[i](h)
	}
	return h.ServeHTTP
}

func (r *Router) ServeHTTP() (http.Handler, error) {
	mux := http.NewServeMux()
	for _, config := range r.routes {
		handler := r.wrap(config.HandlerFunc)
		pattern := fmt.Sprintf("%s %s", config.Method, config.Endpoint)
		if config.Endpoint == "/" {
			mux.HandleFunc(pattern, func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path != "/" {
					r.NotFound(w, req)
					return
				}
				handler(w, req)
			})
		} else {
			mux.HandleFunc(pattern, handler)
		}
	}
	return mux, nil
}
