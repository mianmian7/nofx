package api

import (
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestServerShutdownDrainsActiveRequestsWithoutStartError(t *testing.T) {
	// Reserve an ephemeral port; Start owns its listener, so release it first.
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	s := NewServer(nil, nil, nil, port)
	entered := make(chan struct{})
	release := make(chan struct{})
	s.router.GET("/drain-test", func(c *gin.Context) {
		close(entered)
		<-release
		c.Status(http.StatusNoContent)
	})
	startDone := make(chan error, 1)
	go func() { startDone <- s.Start() }()
	t.Cleanup(func() { _ = s.Shutdown() })
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()

	client := &http.Client{Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	requestDone := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(3 * time.Second)
		for {
			resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/drain-test", port))
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode != http.StatusNoContent {
					err = fmt.Errorf("status = %d, want 204", resp.StatusCode)
				}
				requestDone <- err
				return
			}
			if time.Now().After(deadline) {
				requestDone <- err
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	select {
	case <-entered:
	case err := <-startDone:
		t.Fatalf("server exited before receiving request: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("request did not reach server")
	}

	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- s.Shutdown() }()
	select {
	case err := <-startDone:
		if err != nil {
			t.Fatalf("normal shutdown must not reach main's fatal startup path: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("listener did not stop")
	}
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before the active request drained: %v", err)
	default:
	}
	close(release)
	if err := <-requestDone; err != nil {
		t.Fatalf("active request failed during shutdown: %v", err)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatal(err)
	}
}

func TestServerShutdownBeforeStart(t *testing.T) {
	s := NewServer(nil, nil, nil, 0)
	if s.httpServer == nil {
		t.Fatal("HTTP server must exist before Start so an early shutdown cannot be lost")
	}
	if err := s.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err != nil {
		t.Fatalf("starting a shut-down server must return normally: %v", err)
	}
}

func TestServerStartReturnsListenErrors(t *testing.T) {
	s := NewServer(nil, nil, nil, -1)
	if err := s.Start(); err == nil {
		t.Fatal("invalid listener address must still return an error")
	}
}
