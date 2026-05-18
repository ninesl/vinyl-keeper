package router

import (
	"net/http"
)

type Server struct {
	Router *Router
	Addr   string
}

func NewServer(addr string) *Server {
	return NewServerWithRouter(addr, NewRouter())
}

func NewServerWithRouter(addr string, router *Router) *Server {
	return &Server{
		Router: router,
		Addr:   addr,
	}
}

func (s *Server) ListenAndServe() error {
	return http.ListenAndServe(s.Addr, s.Router)
}

func (s *Server) ListenAndServeTLS(certFile, keyFile string) error {
	return http.ListenAndServeTLS(s.Addr, certFile, keyFile, s.Router)
}
