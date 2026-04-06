package router

import (
	"net/http"
)

type Server struct {
	Router *Router
	Addr   string
}

func NewServer(addr string) *Server {
	return &Server{
		Router: NewRouter(),
		Addr:   addr,
	}
}

func (s *Server) ListenAndServe() error {
	handler, err := s.Router.ServeHTTP()
	if err != nil {
		return err
	}
	return http.ListenAndServe(s.Addr, handler)
}
