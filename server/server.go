package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Server struct {
	proxyAddr string
	certMap   map[string]string
	keyMap    map[string]string
}

func NewServer(proxyAddr string, certMap, keyMap map[string]string) *Server {
	return &Server{proxyAddr: proxyAddr, certMap: certMap, keyMap: keyMap}
}

func (s *Server) handler(w http.ResponseWriter, r *http.Request) {
	if strings.ToLower(r.Header.Get("Connection")) == "upgrade" && strings.ToLower(r.Header.Get("Upgrade")) == "websocket" {
		s.handleWebSocket(w, r)
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	proxyURL, err := url.Parse("http://" + s.proxyAddr + r.RequestURI)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	proxyRequest, err := http.NewRequestWithContext(ctx, r.Method, proxyURL.String(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for key, value := range r.Header {
		proxyRequest.Header[key] = value
	}

	host, _, _ := net.SplitHostPort(r.RemoteAddr)

	proxyRequest.Header.Set("X-Forwarded-For", host)
	proxyRequest.Header.Set("X-Real-Ip", host)
	proxyRequest.Header.Set("Server", "FPS")

	proxyResponse, err := doRequst(proxyRequest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for key, value := range proxyResponse.Header {
		for _, v := range value {
			w.Header().Add(key, v)
		}
	}
	w.Header().Add("Server", "FPS")
	w.WriteHeader(proxyResponse.StatusCode)

	io.Copy(w, proxyResponse.Body)
}

func doRequst(req *http.Request) (*http.Response, error) {
	client := http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	for {
		do, err := client.Do(req)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				fmt.Println("Cancel:", err)
				return nil, err
			}
			fmt.Println("Error:", err)
			time.Sleep(time.Second)
			continue
		}
		return do, nil
	}
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, "Failed to upgrade to WebSocket", http.StatusInternalServerError)
		return
	}
	defer conn.Close()

	proxyURL := "ws://" + s.proxyAddr + r.URL.RequestURI()
	proxyConn, _, err := websocket.DefaultDialer.Dial(proxyURL, nil)
	if err != nil {
		http.Error(w, "Failed to connect to backend WebSocket server", http.StatusInternalServerError)
		return
	}
	defer proxyConn.Close()

	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			messageType, message, err := proxyConn.ReadMessage()
			if err != nil {
				conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
			err = conn.WriteMessage(messageType, message)
			if err != nil {
				return
			}
		}
	}()

	go func() {
		for {
			messageType, message, err := conn.ReadMessage()
			if err != nil {
				proxyConn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
			err = proxyConn.WriteMessage(messageType, message)
			if err != nil {
				return
			}
		}
	}()

	<-done
}

func (s *Server) getCertificate(info *tls.ClientHelloInfo) (*tls.Certificate, error) {
	certFile, certExists := s.certMap[info.ServerName]
	keyFile, keyExists := s.keyMap[info.ServerName]

	if !certExists || !keyExists {
		return nil, fmt.Errorf("no certificate found for domain: %s", info.ServerName)
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	return &cert, nil
}

func (s *Server) Start() {
	handler := http.NewServeMux()
	handler.HandleFunc("/", s.handler)
	tlsConfig := &tls.Config{
		GetCertificate: s.getCertificate,
	}
	httpsServer := http.Server{
		Addr:      ":443",
		Handler:   handler,
		TLSConfig: tlsConfig,
	}

	httpServer := http.Server{
		Addr:    ":80",
		Handler: handler,
	}

	go func() {
		fmt.Println("Listening on port 80")
		httpServer.ListenAndServe()
	}()

	fmt.Println("Listening on port 443")
	httpsServer.ListenAndServeTLS("", "")
}
